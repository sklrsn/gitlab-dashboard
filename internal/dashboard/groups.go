package dashboard

import (
	"context"
	"fmt"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"gitlab-dashboard/internal/cache"
	"gitlab-dashboard/internal/gitlab"
)

const (
	// maxWindowRuns caps how many top-level pipelines one grouped view reads.
	maxWindowRuns = 1000
	// Raw pages of 100 read per project: more for a single project, fewer per
	// project when a whole group is fanned out.
	projectWindowPages = 15
	groupWindowPages   = 3
	// maxChildParents caps the parents whose children are read for "group by child".
	maxChildParents = 200
	historyLen      = 10
	maxOpenRuns     = 100
	olderRunsPage   = 20
)

// GroupBy values accepted by GroupedPage.
var GroupByKinds = map[string]bool{"name": true, "child": true, "ref": true, "source": true, "project": true}

type Window struct {
	Name  string
	Count int           // newest N top-level pipelines, or
	Span  time.Duration // everything created within this span
}

func ParseWindow(s string) (Window, error) {
	switch s {
	case "", "100":
		return Window{Name: "100", Count: 100}, nil
	case "24h":
		return Window{Name: "24h", Span: 24 * time.Hour}, nil
	case "7d":
		return Window{Name: "7d", Span: 7 * 24 * time.Hour}, nil
	}
	return Window{}, fmt.Errorf("unknown window %q", s)
}

type HistoryDot struct {
	Status string `json:"status"`
	ID     int64  `json:"id,omitempty"`
	WebURL string `json:"webUrl,omitempty"`
}

type PipelineGroup struct {
	Key         string       `json:"key"`
	Label       string       `json:"label"`
	Sub         string       `json:"sub,omitempty"`
	Status      string       `json:"status"`
	StatusLabel string       `json:"statusLabel"`
	Limited     bool         `json:"limited,omitempty"`
	LastAt      *time.Time   `json:"lastAt,omitempty"`
	Runs        int          `json:"runs"`
	Passed      int          `json:"passed"`
	Failed      int          `json:"failed"`
	AvgDuration *int         `json:"avgDuration,omitempty"`
	History     []HistoryDot `json:"history"` // oldest → newest
	// Set when the group is one named pipeline in one project, so older runs
	// beyond the window can be loaded with GitLab's name filter.
	// For branch groups: the newest merge request seen on this branch.
	MergeRequest *MRRef `json:"mergeRequest,omitempty"`
	OlderProject string `json:"olderProject,omitempty"`
	OlderName    string `json:"olderName,omitempty"`
	// For an open group whose runs span several branches: one sub-group per
	// branch (keys are the group key + BranchSep + "r:<ref>"). Otherwise Items.
	Branches []PipelineGroup `json:"branches,omitempty"`
	Items    []Pipeline      `json:"items,omitempty"` // only for groups the UI has open
}

// BranchSep joins a group key and a branch sub-group key.
const BranchSep = "\x1f"

type GroupedPage struct {
	Groups    []PipelineGroup `json:"groups"`
	Pipelines int             `json:"pipelines"`
	// Since is the creation time of the oldest pipeline considered.
	Since *time.Time `json:"since,omitempty"`
	// Capped means the window held more than the dashboard reads; groups cover only pipelines since Since.
	Capped   bool     `json:"capped"`
	Warnings []string `json:"warnings,omitempty"`
}

type window struct {
	runs     []Pipeline
	since    *time.Time
	capped   bool
	warnings []string
}

// windowRuns returns the scope's top-level pipelines inside the window, newest first.
func (s *Service) windowRuns(ctx context.Context, kind, path string, w Window, f Filter) (window, error) {
	gf, err := f.gitlab()
	if err != nil {
		return window{}, err
	}
	key := fmt.Sprintf("win|%s|%s|%s|%s|%s", kind, path, w.Name, gf.Ref, gf.Status)
	return cache.Do(ctx, s.short, key, func(ctx context.Context) (window, error) {
		cutoff := time.Time{}
		if w.Span > 0 {
			cutoff = s.now().Add(-w.Span)
		}
		if kind == "project" {
			runs, complete, err := s.projectWindow(ctx, path, w, cutoff, gf, projectWindowPages, topLevel(""))
			if err != nil {
				return window{}, err
			}
			out := finishWindow(w, [][]gitlab.Pipeline{runs}, []bool{complete}, s)
			s.resolveMRs(ctx, out.runs)
			return out, nil
		}

		projects, err := s.groupProjects(ctx, path)
		if err != nil {
			return window{}, err
		}
		type result struct {
			runs     []gitlab.Pipeline
			complete bool
			err      error
		}
		results := make([]result, len(projects))
		parallel(len(projects), func(i int) {
			r, c, err := s.projectWindow(ctx, projects[i].PathWithNamespace, w, cutoff, gf, groupWindowPages, topLevel(path))
			results[i] = result{r, c, err}
		})
		var lists [][]gitlab.Pipeline
		var complete []bool
		var failed []string
		var firstErr error
		for i, r := range results {
			if r.err != nil {
				failed = append(failed, projects[i].PathWithNamespace)
				firstErr = cmpErr(firstErr, r.err)
				continue
			}
			lists = append(lists, r.runs)
			complete = append(complete, r.complete)
		}
		if len(projects) > 0 && len(failed) == len(projects) {
			return window{}, firstErr
		}
		out := finishWindow(w, lists, complete, s)
		s.resolveMRs(ctx, out.runs)
		out.warnings = s.groupWarnings(len(projects), failed)
		return out, nil
	})
}

// topLevel keeps pipelines that are listed at the top level: not same-project
// children, and (in a group) not downstreams of a pipeline in the same group.
func topLevel(group string) func(gitlab.Pipeline) bool {
	return func(p gitlab.Pipeline) bool {
		if p.Source == "parent_pipeline" {
			return false
		}
		if group != "" && p.Source == "pipeline" && p.Upstream != nil && strings.HasPrefix(p.Upstream.Project.FullPath, group+"/") {
			return false
		}
		return true
	}
}

// projectWindow pages through one project's pipelines until the window is
// covered. complete is false when it stopped at maxPages first.
func (s *Service) projectWindow(ctx context.Context, path string, w Window, cutoff time.Time, gf gitlab.PipelineFilter, maxPages int, keep func(gitlab.Pipeline) bool) (out []gitlab.Pipeline, complete bool, err error) {
	cursor := ""
	for range maxPages {
		pg, err := s.projectPipelines(ctx, path, 100, cursor, gf)
		if err != nil {
			return nil, false, err
		}
		for _, p := range pg.Pipelines {
			if w.Span > 0 && p.CreatedAt != nil && p.CreatedAt.Before(cutoff) {
				return out, true, nil
			}
			if keep(p) {
				out = append(out, p)
				if w.Count > 0 && len(out) >= w.Count {
					return out, true, nil
				}
			}
		}
		if !pg.HasNextPage {
			return out, true, nil
		}
		cursor = pg.EndCursor
	}
	return out, false, nil
}

// finishWindow merges per-project lists. When a project stopped early, every
// list is cut at the newest point that project reached, so all projects
// cover the same period.
func finishWindow(w Window, lists [][]gitlab.Pipeline, complete []bool, s *Service) window {
	var since time.Time
	capped := false
	for i, l := range lists {
		if !complete[i] && len(l) > 0 {
			capped = true
			if t := l[len(l)-1].CreatedAt; t != nil && t.After(since) {
				since = *t
			}
		}
	}
	var all []Pipeline
	for _, l := range lists {
		for _, p := range l {
			if capped && p.CreatedAt != nil && p.CreatedAt.Before(since) {
				continue
			}
			all = append(all, convert(s.base, p))
		}
	}
	sort.Slice(all, func(i, j int) bool { return all[i].ID > all[j].ID })
	if w.Count > 0 && len(all) > w.Count {
		all = all[:w.Count]
	}
	if len(all) > maxWindowRuns {
		all = all[:maxWindowRuns]
		capped = true
	}
	out := window{runs: all, capped: capped}
	if len(all) > 0 {
		out.since = all[len(all)-1].CreatedAt
	}
	return out
}

// GroupedPage groups the window's pipelines. Runs are included only for the
// groups listed in open, so polling stays light.
func (s *Service) GroupedPage(ctx context.Context, kind, path, by string, w Window, f Filter, open map[string]bool) (GroupedPage, error) {
	if !GroupByKinds[by] {
		return GroupedPage{}, fmt.Errorf("unknown grouping %q", by)
	}
	win, err := s.windowRuns(ctx, kind, path, w, f)
	if err != nil {
		return GroupedPage{}, err
	}
	out := GroupedPage{Pipelines: len(win.runs), Since: win.since, Capped: win.capped, Warnings: win.warnings}
	runs := win.runs
	if by == "child" {
		var warn []string
		runs, warn = s.childRuns(ctx, win.runs)
		out.Warnings = append(out.Warnings, warn...)
	}
	out.Groups = buildGroups(by, runs, open, "")
	return out, nil
}

// childRuns lists the child and downstream pipelines started by the window's
// pipelines, at any depth, each pointing back at its parent.
func (s *Service) childRuns(ctx context.Context, parents []Pipeline) ([]Pipeline, []string) {
	var out []Pipeline
	var warnings []string
	budget, failed := maxChildParents, 0
	level := parents
	for len(level) > 0 && budget > 0 {
		var todo []Pipeline
		for _, p := range level {
			if p.ChildCount > 0 && !p.Limited && p.ID > 0 && p.IID != "" {
				todo = append(todo, p)
			}
		}
		if len(todo) > budget {
			todo = todo[:budget]
			warnings = append(warnings, fmt.Sprintf("Child pipelines were read for the newest %d parent pipelines only.", maxChildParents))
		}
		budget -= len(todo)
		kids := make([][]Pipeline, len(todo))
		errs := make([]error, len(todo))
		parallel(len(todo), func(i int) { kids[i], errs[i] = s.childrenOf(ctx, todo[i]) })
		var next []Pipeline
		for i, p := range todo {
			if errs[i] != nil {
				failed++
				continue
			}
			for _, k := range kids[i] {
				k.Parent = &ParentRef{ID: p.ID, Title: p.Title, WebURL: p.WebURL}
				out = append(out, k)
				next = append(next, k)
			}
		}
		level = next
	}
	if failed > 0 {
		warnings = append(warnings, fmt.Sprintf("Child pipelines of %d pipeline(s) could not be read.", failed))
	}
	return out, warnings
}

// childrenOf reuses Children, caching longer for parents that finished a while ago.
func (s *Service) childrenOf(ctx context.Context, p Pipeline) ([]Pipeline, error) {
	if finished(p.Status) && p.FinishedAt != nil && s.now().Sub(*p.FinishedAt) > 10*time.Minute {
		key := fmt.Sprintf("chl|%s|%s|%d", p.Project.Path, p.IID, p.ID)
		return cache.Do(ctx, s.long, key, func(ctx context.Context) ([]Pipeline, error) {
			return s.Children(ctx, p.Project.Path, p.IID, p.ID)
		})
	}
	return s.Children(ctx, p.Project.Path, p.IID, p.ID)
}

func finished(status string) bool {
	switch status {
	case "success", "success-with-warnings", "failed", "canceled", "skipped":
		return true
	}
	return false
}

func passed(status string) bool { return status == "success" || status == "success-with-warnings" }

var sourceLabels = map[string]string{
	"push": "push", "web": "run from UI", "trigger": "trigger token", "schedule": "schedule", "api": "API",
	"external": "external", "pipeline": "multi-project", "parent_pipeline": "child", "chat": "chat",
	"webide": "Web IDE", "merge_request_event": "merge request", "external_pull_request_event": "external pull request",
	"ondemand_dast_scan": "DAST scan", "ondemand_dast_validation": "DAST validation", "security_orchestration_policy": "security policy",
}

func sourceLabel(src string) string {
	if l, ok := sourceLabels[src]; ok {
		return l
	}
	if src == "" {
		return "unknown source"
	}
	return strings.ReplaceAll(src, "_", " ")
}

func groupKey(by string, p Pipeline) (key, label, sub string) {
	switch by {
	case "name":
		if p.Name != "" {
			return "n:" + p.Name, p.Name, ""
		}
		return "u:" + p.Source, "(unnamed) · " + sourceLabel(p.Source), ""
	case "ref":
		b := orStr(p.Branch, p.Ref)
		if b == "" {
			return "r:", "(no ref)", ""
		}
		return "r:" + b, b, ""
	case "source":
		return "s:" + p.Source, sourceLabel(p.Source), ""
	case "project":
		return "p:" + p.Project.Path, p.Project.Path, ""
	default: // child
		if p.CrossProject && p.Project.Path != "" {
			return "c:" + p.Title + "@" + p.Project.Path, p.Title, p.Project.Path
		}
		return "c:" + p.Title, p.Title, ""
	}
}

// buildGroups groups runs (newest first). Groups whose latest run failed come
// first, then the most recently active. An open top-level group is split by
// branch when its runs span more than one; prefix is set for those sub-groups.
func buildGroups(by string, runs []Pipeline, open map[string]bool, prefix string) []PipelineGroup {
	idx := map[string]int{}
	var gs []PipelineGroup
	type acc struct {
		durSum, durN int
		mixed        bool
		runs         []Pipeline
	}
	var accs []acc
	for _, p := range runs {
		k, label, sub := groupKey(by, p)
		k = prefix + k
		i, ok := idx[k]
		if !ok {
			i = len(gs)
			idx[k] = i
			gs = append(gs, PipelineGroup{Key: k, Label: label, Sub: sub, Status: p.Status, StatusLabel: p.StatusLabel,
				Limited: p.Limited && p.ID == 0, OlderProject: p.Project.Path, History: []HistoryDot{}})
			accs = append(accs, acc{})
		}
		g, a := &gs[i], &accs[i]
		g.Runs++
		if g.LastAt == nil {
			g.LastAt = p.CreatedAt
		}
		if passed(p.Status) {
			g.Passed++
		} else if p.Status == "failed" {
			g.Failed++
		}
		if p.Duration != nil {
			a.durSum += *p.Duration
			a.durN++
		}
		if p.Project.Path != g.OlderProject {
			a.mixed = true
		}
		if len(g.History) < historyLen {
			g.History = append(g.History, HistoryDot{Status: p.Status, ID: p.ID, WebURL: p.WebURL})
		}
		if by == "ref" && g.MergeRequest == nil && p.MergeRequest != nil {
			g.MergeRequest = p.MergeRequest
		}
		if open[k] {
			a.runs = append(a.runs, p)
		}
	}
	for i := range gs {
		g, a := &gs[i], accs[i]
		if a.runs != nil {
			if prefix == "" && by != "ref" {
				if br := buildGroups("ref", a.runs, open, g.Key+BranchSep); len(br) > 1 {
					g.Branches = br
				}
			}
			if g.Branches == nil {
				g.Items = a.runs[:min(len(a.runs), maxOpenRuns)]
			}
		}
		if a.durN > 0 {
			avg := a.durSum / a.durN
			g.AvgDuration = &avg
		}
		for l, r := 0, len(g.History)-1; l < r; l, r = l+1, r-1 {
			g.History[l], g.History[r] = g.History[r], g.History[l]
		}
		if by == "name" && strings.HasPrefix(g.Key, "n:") && !a.mixed && g.OlderProject != "" {
			g.OlderName = g.Label
		} else {
			g.OlderProject = ""
		}
	}
	sort.SliceStable(gs, func(i, j int) bool {
		fi, fj := gs[i].Status == "failed", gs[j].Status == "failed"
		if fi != fj {
			return fi
		}
		ti, tj := gs[i].LastAt, gs[j].LastAt
		switch {
		case ti == nil || tj == nil:
			return ti != nil && tj == nil
		case !ti.Equal(*tj):
			return ti.After(*tj)
		}
		return gs[i].Label < gs[j].Label
	})
	if gs == nil {
		gs = []PipelineGroup{}
	}
	return gs
}

// OlderRuns pages through a named pipeline's runs with GitLab's REST name
// filter. These rows carry less detail (no stages or duration).
func (s *Service) OlderRuns(ctx context.Context, project, name string, page int) (Page, error) {
	key := fmt.Sprintf("old|%s|%s|%d", project, name, page)
	return cache.Do(ctx, s.short, key, func(ctx context.Context) (Page, error) {
		list, err := s.gl.PipelinesByName(ctx, project, name, page, olderRunsPage)
		if err != nil {
			return Page{}, err
		}
		out := Page{Pipelines: []Pipeline{}, HasMore: len(list) == olderRunsPage}
		if out.HasMore {
			out.NextCursor = strconv.Itoa(page + 1)
		}
		for _, p := range list {
			out.Pipelines = append(out.Pipelines, Pipeline{
				Key: "p" + strconv.FormatInt(p.ID, 10), ID: p.ID, IID: strconv.FormatInt(p.IID, 10), Name: p.Name, Title: orStr(p.Name, "Pipeline #"+strconv.FormatInt(p.ID, 10)),
				Status: strings.ToLower(p.Status), StatusLabel: restLabel(p.Status), Source: p.Source, Ref: p.Ref, SHA: p.SHA,
				WebURL: absURL(s.base, p.WebURL), Project: ProjectRef{Path: project}, CreatedAt: p.CreatedAt, Stages: []Stage{},
			})
		}
		s.resolveMRs(ctx, out.Pipelines)
		return out, nil
	})
}

func restLabel(status string) string {
	if status == "success" {
		return "passed"
	}
	return strings.ReplaceAll(strings.ToLower(status), "_", " ")
}

func orStr(v, d string) string {
	if v == "" {
		return d
	}
	return v
}

// parallel runs fn(0..n-1) with at most fanoutParallel at once.
func parallel(n int, fn func(i int)) {
	sem := make(chan struct{}, fanoutParallel)
	var wg sync.WaitGroup
	for i := range n {
		wg.Add(1)
		go func() {
			defer wg.Done()
			sem <- struct{}{}
			defer func() { <-sem }()
			fn(i)
		}()
	}
	wg.Wait()
}
