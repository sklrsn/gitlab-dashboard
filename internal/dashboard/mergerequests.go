package dashboard

import (
	"context"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"gitlab-dashboard/internal/gitlab"
)

// Merge request pipelines run on refs/merge-requests/<iid>/head (the source
// branch), …/merge (merged results) or …/train (merge trains).
var mrRef = regexp.MustCompile(`^refs/merge-requests/(\d+)/(?:head|merge|train)$`)

const mrTTL = 5 * time.Minute

type mrEntry struct {
	mr  *gitlab.MergeRequest // nil: GitLab doesn't know it (or the token can't see it)
	exp time.Time
}

type mrStore struct {
	mu sync.Mutex
	m  map[string]mrEntry
}

func mrKey(project string, iid int64) string { return project + "!" + strconv.FormatInt(iid, 10) }

// resolveMRs labels merge request pipelines with their source branch. Lookups
// are batched per project (one REST call for up to 100 merge requests) and
// cached. If a lookup fails, the pipeline keeps "!<iid>" as its branch.
func (s *Service) resolveMRs(ctx context.Context, ps []Pipeline) {
	type want struct {
		idx int
		iid int64
	}
	byProject := map[string][]want{}
	for i := range ps {
		p := &ps[i]
		if p.Branch == "" {
			p.Branch = p.Ref
		}
		m := mrRef.FindStringSubmatch(p.Ref)
		if m == nil || p.Project.Path == "" {
			continue
		}
		iid, _ := strconv.ParseInt(m[1], 10, 64)
		p.MergeRequest = &MRRef{IID: iid}
		if p.Project.WebURL != "" {
			p.MergeRequest.WebURL = strings.TrimRight(p.Project.WebURL, "/") + "/-/merge_requests/" + m[1]
		}
		p.Branch = "!" + m[1]
		byProject[p.Project.Path] = append(byProject[p.Project.Path], want{i, iid})
	}
	if len(byProject) == 0 {
		return
	}

	now := s.now()
	found := map[string]*gitlab.MergeRequest{}
	missing := map[string][]int64{}
	s.mrs.mu.Lock()
	for project, ws := range byProject {
		seen := map[int64]bool{}
		for _, w := range ws {
			k := mrKey(project, w.iid)
			if e, ok := s.mrs.m[k]; ok && now.Before(e.exp) {
				found[k] = e.mr
			} else if !seen[w.iid] {
				seen[w.iid] = true
				missing[project] = append(missing[project], w.iid)
			}
		}
	}
	s.mrs.mu.Unlock()

	var mu sync.Mutex
	projects := make([]string, 0, len(missing))
	for p := range missing {
		projects = append(projects, p)
	}
	sort.Strings(projects)
	parallel(len(projects), func(i int) {
		project := projects[i]
		iids := missing[project]
		for start := 0; start < len(iids); start += 100 {
			chunk := iids[start:min(start+100, len(iids))]
			mrs, err := s.gl.MergeRequestsByIID(ctx, project, chunk)
			if err != nil {
				continue // leave "!<iid>"; try again next time
			}
			byIID := map[int64]*gitlab.MergeRequest{}
			for j := range mrs {
				byIID[mrs[j].IID] = &mrs[j]
			}
			mu.Lock()
			s.mrs.mu.Lock()
			for _, iid := range chunk {
				k := mrKey(project, iid)
				found[k] = byIID[iid]
				s.mrs.m[k] = mrEntry{byIID[iid], now.Add(mrTTL)}
			}
			s.mrs.mu.Unlock()
			mu.Unlock()
		}
	})

	for project, ws := range byProject {
		for _, w := range ws {
			mr := found[mrKey(project, w.iid)]
			if mr == nil || mr.SourceBranch == "" {
				continue
			}
			p := &ps[w.idx]
			p.Branch = mr.SourceBranch
			p.MergeRequest = &MRRef{IID: mr.IID, Title: mr.Title, SourceBranch: mr.SourceBranch, TargetBranch: mr.TargetBranch,
				WebURL: orStr(absURL(s.base, mr.WebURL), p.MergeRequest.WebURL)}
		}
	}
}
