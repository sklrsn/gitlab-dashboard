// Package dashboard turns GitLab API data into the pipeline views the UI shows.
package dashboard

import (
	"context"
	"errors"
	"fmt"
	"net/url"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"gitlab-dashboard/internal/cache"
	"gitlab-dashboard/internal/gitlab"
)

const (
	pageSize       = 20
	maxGroupRows   = 100
	fanoutParallel = 8
)

// Statuses accepted by the status filter, mapped to GitLab's PipelineStatusEnum.
var statusFilter = map[string]string{
	"created": "CREATED", "waiting_for_resource": "WAITING_FOR_RESOURCE", "preparing": "PREPARING",
	"pending": "PENDING", "running": "RUNNING", "success": "SUCCESS", "failed": "FAILED",
	"canceled": "CANCELED", "skipped": "SKIPPED", "manual": "MANUAL", "scheduled": "SCHEDULED",
}

type Filter struct {
	Ref    string
	Status string
}

func (f Filter) gitlab() (gitlab.PipelineFilter, error) {
	out := gitlab.PipelineFilter{Ref: f.Ref}
	if f.Status != "" {
		s, ok := statusFilter[f.Status]
		if !ok {
			return out, fmt.Errorf("unknown status %q", f.Status)
		}
		out.Status = s
	}
	return out, nil
}

type Page struct {
	Pipelines []Pipeline `json:"pipelines"`
	// NextCursor continues a project list; HasMore + a larger limit continues a group list.
	NextCursor string   `json:"nextCursor,omitempty"`
	HasMore    bool     `json:"hasMore"`
	Projects   int      `json:"projects,omitempty"`
	Warnings   []string `json:"warnings,omitempty"`
}

type ScopeItem struct {
	Kind   string `json:"kind"`
	Path   string `json:"path"`
	Name   string `json:"name"`
	WebURL string `json:"webUrl"`
}

type Service struct {
	gl     *gitlab.Client
	base   *url.URL
	fanout int
	short  *cache.Cache
	long   *cache.Cache
}

func NewService(gl *gitlab.Client, ttl time.Duration, fanout int) *Service {
	return &Service{gl: gl, base: gl.BaseURL(), fanout: fanout, short: cache.New(ttl), long: cache.New(5 * time.Minute)}
}

func (s *Service) projectPipelines(ctx context.Context, path string, first int, after string, f gitlab.PipelineFilter) (gitlab.PipelinePage, error) {
	key := fmt.Sprintf("pp|%s|%d|%s|%s|%s", path, first, after, f.Ref, f.Status)
	return cache.Do(ctx, s.short, key, func(ctx context.Context) (gitlab.PipelinePage, error) {
		return s.gl.ProjectPipelines(ctx, path, first, after, f)
	})
}

// ProjectPage lists top-level pipelines of one project. Same-project child
// pipelines are hidden here; they appear when their parent is expanded.
func (s *Service) ProjectPage(ctx context.Context, path string, f Filter, after string) (Page, error) {
	gf, err := f.gitlab()
	if err != nil {
		return Page{}, err
	}
	var out Page
	cursor := after
	for range 3 {
		pg, err := s.projectPipelines(ctx, path, 30, cursor, gf)
		if err != nil {
			return Page{}, err
		}
		for _, p := range pg.Pipelines {
			if p.Source != "parent_pipeline" {
				out.Pipelines = append(out.Pipelines, convert(s.base, p))
			}
		}
		cursor, out.HasMore = pg.EndCursor, pg.HasNextPage
		if !pg.HasNextPage || len(out.Pipelines) >= pageSize {
			break
		}
	}
	if out.HasMore {
		out.NextCursor = cursor
	}
	if out.Pipelines == nil {
		out.Pipelines = []Pipeline{}
	}
	return out, nil
}

// GroupPage merges the newest pipelines of the group's most active projects.
// limit grows as the user clicks "load more"; there's no server-side state.
func (s *Service) GroupPage(ctx context.Context, group string, f Filter, limit int) (Page, error) {
	gf, err := f.gitlab()
	if err != nil {
		return Page{}, err
	}
	limit = min(max(limit, pageSize), maxGroupRows)
	projects, err := cache.Do(ctx, s.long, "gp|"+group+"|"+strconv.Itoa(s.fanout), func(ctx context.Context) ([]gitlab.Project, error) {
		return s.gl.GroupProjects(ctx, group, s.fanout)
	})
	if err != nil {
		return Page{}, err
	}

	type result struct {
		page gitlab.PipelinePage
		err  error
	}
	results := make([]result, len(projects))
	sem := make(chan struct{}, fanoutParallel)
	var wg sync.WaitGroup
	for i, p := range projects {
		wg.Add(1)
		go func() {
			defer wg.Done()
			sem <- struct{}{}
			defer func() { <-sem }()
			pg, err := s.projectPipelines(ctx, p.PathWithNamespace, limit, "", gf)
			results[i] = result{pg, err}
		}()
	}
	wg.Wait()

	out := Page{Projects: len(projects), Pipelines: []Pipeline{}}
	var failed []string
	var firstErr error
	moreUpstream := false
	prefix := group + "/"
	for i, r := range results {
		if r.err != nil {
			failed = append(failed, projects[i].PathWithNamespace)
			firstErr = cmpErr(firstErr, r.err)
			continue
		}
		moreUpstream = moreUpstream || r.page.HasNextPage
		for _, p := range r.page.Pipelines {
			if p.Source == "parent_pipeline" {
				continue
			}
			// Downstream pipelines whose parent is in this group show up under that parent.
			if p.Source == "pipeline" && p.Upstream != nil && strings.HasPrefix(p.Upstream.Project.FullPath, prefix) {
				continue
			}
			out.Pipelines = append(out.Pipelines, convert(s.base, p))
		}
	}
	if len(projects) > 0 && len(failed) == len(projects) {
		return Page{}, firstErr
	}
	if len(failed) > 0 {
		out.Warnings = append(out.Warnings, fmt.Sprintf("%d project(s) could not be loaded: %s", len(failed), strings.Join(first(failed, 5), ", ")))
	}
	if len(projects) == s.fanout {
		out.Warnings = append(out.Warnings, fmt.Sprintf("Showing the %d most recently active projects in this group. Pick a project to see others.", s.fanout))
	}

	sort.Slice(out.Pipelines, func(i, j int) bool { return out.Pipelines[i].ID > out.Pipelines[j].ID })
	if len(out.Pipelines) > limit {
		out.Pipelines = out.Pipelines[:limit]
		out.HasMore = true
	} else {
		out.HasMore = moreUpstream
	}
	if limit >= maxGroupRows {
		out.HasMore = false
	}
	return out, nil
}

// Children lists the pipelines a pipeline triggered: same-project child
// pipelines and multi-project downstream pipelines, labeled by trigger job.
func (s *Service) Children(ctx context.Context, project, iid string, id int64) ([]Pipeline, error) {
	key := fmt.Sprintf("ch|%s|%s|%d", project, iid, id)
	return cache.Do(ctx, s.short, key, func(ctx context.Context) ([]Pipeline, error) {
		var (
			wg                sync.WaitGroup
			down              []gitlab.Pipeline
			bridges           []gitlab.Bridge
			downErr, bridgErr error
		)
		wg.Add(2)
		go func() { defer wg.Done(); down, downErr = s.gl.Downstream(ctx, project, iid) }()
		go func() { defer wg.Done(); bridges, bridgErr = s.gl.Bridges(ctx, project, id) }()
		wg.Wait()
		if downErr != nil && bridgErr != nil {
			return nil, downErr
		}
		return mergeChildren(s.base, project, down, bridges), nil
	})
}

func mergeChildren(base *url.URL, parentProject string, down []gitlab.Pipeline, bridges []gitlab.Bridge) []Pipeline {
	byID := make(map[int64]gitlab.Pipeline, len(down))
	for _, p := range down {
		byID[gitlab.NumericID(p.ID)] = p
	}
	sort.Slice(bridges, func(i, j int) bool { return bridges[i].ID < bridges[j].ID })

	out := []Pipeline{}
	used := map[int64]bool{}
	for _, b := range bridges {
		d := b.DownstreamPipeline
		switch {
		case d == nil:
			// Trigger job hasn't started its pipeline yet (manual, pending, skipped...).
			out = append(out, Pipeline{
				Key: "b" + strconv.FormatInt(b.ID, 10), Title: b.Name, TriggerJob: b.Name,
				Status: b.Status, StatusLabel: bridgeLabel(b.Status), WebURL: b.WebURL,
				Stages: []Stage{}, Limited: true,
			})
		case hasKey(byID, d.ID):
			p := convert(base, byID[d.ID])
			p.TriggerJob = b.Name
			p.Title = title(p)
			p.CrossProject = p.Project.Path != parentProject
			used[d.ID] = true
			out = append(out, p)
		default:
			// The token can see the trigger job but not the downstream project.
			out = append(out, Pipeline{
				Key: "p" + strconv.FormatInt(d.ID, 10), ID: d.ID, Title: b.Name, TriggerJob: b.Name,
				Status: d.Status, StatusLabel: d.Status, Ref: d.Ref, SHA: d.SHA, WebURL: d.WebURL,
				CreatedAt: d.CreatedAt, Stages: []Stage{}, CrossProject: true, Limited: true,
			})
			used[d.ID] = true
		}
	}
	for _, p := range down {
		if id := gitlab.NumericID(p.ID); !used[id] {
			c := convert(base, p)
			c.CrossProject = c.Project.Path != parentProject
			out = append(out, c)
		}
	}
	return out
}

func (s *Service) Scopes(ctx context.Context, search string) ([]ScopeItem, error) {
	return cache.Do(ctx, s.long, "sc|"+search, func(ctx context.Context) ([]ScopeItem, error) {
		var (
			wg         sync.WaitGroup
			groups     []gitlab.Group
			projects   []gitlab.Project
			gErr, pErr error
		)
		wg.Add(2)
		go func() { defer wg.Done(); groups, gErr = s.gl.SearchGroups(ctx, search, 10) }()
		go func() { defer wg.Done(); projects, pErr = s.gl.SearchProjects(ctx, search, 10) }()
		wg.Wait()
		if gErr != nil && pErr != nil {
			return nil, gErr
		}
		out := []ScopeItem{}
		for _, g := range groups {
			out = append(out, ScopeItem{Kind: "group", Path: g.FullPath, Name: g.FullName, WebURL: g.WebURL})
		}
		for _, p := range projects {
			out = append(out, ScopeItem{Kind: "project", Path: p.PathWithNamespace, Name: p.NameWithNamespace, WebURL: p.WebURL})
		}
		return out, nil
	})
}

func bridgeLabel(status string) string {
	if status == "manual" {
		return "manual trigger"
	}
	return status
}

func hasKey[K comparable, V any](m map[K]V, k K) bool { _, ok := m[k]; return ok }

func first(s []string, n int) []string {
	if len(s) > n {
		return append(s[:n:n], "…")
	}
	return s
}

// cmpErr keeps the most informative error: auth/rate-limit problems over generic ones.
func cmpErr(cur, next error) error {
	if cur == nil {
		return next
	}
	var c, n *gitlab.Error
	if errors.As(next, &n) && (!errors.As(cur, &c) || n.Status == 401 || n.Status == 429) {
		return next
	}
	return cur
}
