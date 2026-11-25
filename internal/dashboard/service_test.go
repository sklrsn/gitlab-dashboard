package dashboard

import (
	"context"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	"gitlab-dashboard/internal/fakegitlab"
	"gitlab-dashboard/internal/gitlab"
)

func setup(t *testing.T, token string) (*Service, *fakegitlab.Server) {
	t.Helper()
	fake := fakegitlab.Demo("good-token", time.Now())
	ts := httptest.NewServer(fake)
	t.Cleanup(ts.Close)
	u, _ := url.Parse(ts.URL)
	gl, err := gitlab.New(gitlab.Options{BaseURL: u, Token: token, Timeout: 5 * time.Second})
	if err != nil {
		t.Fatal(err)
	}
	return NewService(gl, time.Minute, 50), fake
}

func ids(ps []Pipeline) []int64 {
	out := make([]int64, len(ps))
	for i, p := range ps {
		out[i] = p.ID
	}
	return out
}

func has(ps []Pipeline, id int64) bool {
	for _, p := range ps {
		if p.ID == id {
			return true
		}
	}
	return false
}

func TestProjectPageHidesChildPipelines(t *testing.T) {
	svc, _ := setup(t, "good-token")
	pg, err := svc.ProjectPage(context.Background(), "platform/api", Filter{}, "")
	if err != nil {
		t.Fatal(err)
	}
	for _, child := range []int64{48202, 48203, 48214, 48215, 48216, 48217} {
		if has(pg.Pipelines, child) {
			t.Errorf("child pipeline %d listed at top level", child)
		}
	}
	if !has(pg.Pipelines, 48213) || pg.Pipelines[0].ID != 48213 {
		t.Fatalf("want newest parent 48213 first, got %v", ids(pg.Pipelines))
	}
	top := pg.Pipelines[0]
	if top.ChildCount != 3 || top.Title != "Nightly release" || top.Status != "failed" || len(top.Stages) != 4 {
		t.Errorf("unexpected top row: %+v", top)
	}
	if !strings.HasSuffix(top.WebURL, "/platform/api/-/pipelines/48213") {
		t.Errorf("web URL = %q", top.WebURL)
	}
}

func TestProjectPagePagination(t *testing.T) {
	svc, _ := setup(t, "good-token")
	ctx := context.Background()
	first, err := svc.ProjectPage(ctx, "platform/api", Filter{}, "")
	if err != nil {
		t.Fatal(err)
	}
	if len(first.Pipelines) < 20 || !first.HasMore || first.NextCursor == "" {
		t.Fatalf("page 1: %d rows, hasMore=%v cursor=%q", len(first.Pipelines), first.HasMore, first.NextCursor)
	}
	second, err := svc.ProjectPage(ctx, "platform/api", Filter{}, first.NextCursor)
	if err != nil {
		t.Fatal(err)
	}
	if second.HasMore || len(first.Pipelines)+len(second.Pipelines) != 42 {
		t.Fatalf("want 42 top-level pipelines over 2 pages, got %d+%d hasMore=%v", len(first.Pipelines), len(second.Pipelines), second.HasMore)
	}
	if second.Pipelines[0].ID >= first.Pipelines[len(first.Pipelines)-1].ID {
		t.Error("page 2 overlaps page 1")
	}
}

func TestProjectPageFilters(t *testing.T) {
	svc, _ := setup(t, "good-token")
	pg, err := svc.ProjectPage(context.Background(), "platform/api", Filter{Status: "failed"}, "")
	if err != nil {
		t.Fatal(err)
	}
	for _, p := range pg.Pipelines {
		if p.Status != "failed" {
			t.Errorf("status filter leaked %d (%s)", p.ID, p.Status)
		}
	}
	if _, err := svc.ProjectPage(context.Background(), "platform/api", Filter{Status: "bogus"}, ""); err == nil {
		t.Error("expected error for unknown status")
	}
}

func TestDownstreamProjectShowsUpstream(t *testing.T) {
	svc, _ := setup(t, "good-token")
	pg, err := svc.ProjectPage(context.Background(), "platform/deploy", Filter{}, "")
	if err != nil {
		t.Fatal(err)
	}
	if !has(pg.Pipelines, 48204) {
		t.Fatalf("multi-project downstream should be top level in its own project: %v", ids(pg.Pipelines))
	}
	for _, p := range pg.Pipelines {
		if p.ID == 48204 && (p.Upstream == nil || p.Upstream.ID != 48201 || p.Upstream.ProjectPath != "platform/api") {
			t.Errorf("missing upstream link: %+v", p.Upstream)
		}
	}
}

func TestGroupPageMergesProjects(t *testing.T) {
	svc, _ := setup(t, "good-token")
	pg, err := svc.GroupPage(context.Background(), "platform", Filter{}, 20)
	if err != nil {
		t.Fatal(err)
	}
	if pg.Projects != 5 {
		t.Errorf("want 5 visible projects incl. subgroup, got %d", pg.Projects)
	}
	got := ids(pg.Pipelines)
	for i := 1; i < len(got); i++ {
		if got[i] > got[i-1] {
			t.Fatalf("not sorted newest first: %v", got)
		}
	}
	if len(got) != 20 || !pg.HasMore {
		t.Errorf("want 20 rows and more available, got %d hasMore=%v", len(got), pg.HasMore)
	}
	if got[0] != 48222 {
		t.Errorf("want subgroup project pipeline 48222 first, got %d", got[0])
	}
	if has(pg.Pipelines, 48204) {
		t.Error("downstream of a pipeline in the same group should be nested, not top level")
	}
	if has(pg.Pipelines, 48220) {
		t.Error("child pipeline listed at top level")
	}

	more, err := svc.GroupPage(context.Background(), "platform", Filter{}, 40)
	if err != nil {
		t.Fatal(err)
	}
	if len(more.Pipelines) != 40 || !more.HasMore || more.Pipelines[19].ID != pg.Pipelines[19].ID {
		t.Errorf("limit 40: %d rows, hasMore=%v", len(more.Pipelines), more.HasMore)
	}
	all, err := svc.GroupPage(context.Background(), "platform", Filter{}, 100)
	if err != nil {
		t.Fatal(err)
	}
	if len(all.Pipelines) != 48 || all.HasMore {
		t.Errorf("limit 100 should return all 48: %d rows, hasMore=%v", len(all.Pipelines), all.HasMore)
	}
}

func TestChildrenMergesBridgesAndDownstream(t *testing.T) {
	svc, _ := setup(t, "good-token")
	kids, err := svc.Children(context.Background(), "platform/api", "0", 0)
	if err == nil && len(kids) > 0 {
		t.Fatal("bogus pipeline should not yield children")
	}

	pg, err := svc.ProjectPage(context.Background(), "platform/api", Filter{}, "")
	if err != nil {
		t.Fatal(err)
	}
	parent := pg.Pipelines[0]
	kids, err = svc.Children(context.Background(), "platform/api", parent.IID, parent.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(kids) != 4 {
		t.Fatalf("want 4 children (3 started + 1 skipped trigger), got %d: %+v", len(kids), kids)
	}
	byTitle := map[string]Pipeline{}
	for _, k := range kids {
		byTitle[k.Title] = k
	}

	if k := byTitle["build-images"]; k.ID != 48214 || k.Status != "success" || k.TriggerJob != "build-images" || k.CrossProject {
		t.Errorf("unnamed child should be titled by its trigger job: %+v", k)
	}
	if k := byTitle["Integration tests"]; k.ID != 48215 || k.ChildCount != 2 || k.TriggerJob != "integration-tests" {
		t.Errorf("named nested child: %+v", k)
	}
	if k := byTitle["vault-sync"]; !k.Limited || !k.CrossProject || k.ID != 48218 || k.WebURL == "" {
		t.Errorf("hidden downstream should be a limited row: %+v", k)
	}
	if k := byTitle["deploy-production"]; !k.Limited || k.Status != "skipped" || k.ID != 0 || !strings.HasPrefix(k.Key, "b") {
		t.Errorf("unstarted trigger should be a placeholder row: %+v", k)
	}

	nested, err := svc.Children(context.Background(), "platform/api", byTitle["Integration tests"].IID, 48215)
	if err != nil {
		t.Fatal(err)
	}
	if len(nested) != 2 || nested[0].Title != "e2e-shard-1" || nested[0].Status != "failed" {
		t.Errorf("nested children: %+v", nested)
	}
}

func TestCrossProjectChild(t *testing.T) {
	svc, _ := setup(t, "good-token")
	var parent Pipeline
	pg, _ := svc.ProjectPage(context.Background(), "platform/api", Filter{}, "")
	for _, p := range pg.Pipelines {
		if p.ID == 48201 {
			parent = p
		}
	}
	kids, err := svc.Children(context.Background(), "platform/api", parent.IID, parent.ID)
	if err != nil {
		t.Fatal(err)
	}
	last := kids[len(kids)-1]
	if last.ID != 48204 || !last.CrossProject || last.Limited || last.Project.Path != "platform/deploy" || last.Title != "Deploy staging" {
		t.Errorf("visible multi-project child: %+v", last)
	}
}

func TestBadTokenIsReported(t *testing.T) {
	svc, _ := setup(t, "wrong")
	_, err := svc.ProjectPage(context.Background(), "platform/api", Filter{}, "")
	if err == nil || !strings.Contains(err.Error(), "rejected the token") {
		t.Fatalf("want token error, got %v", err)
	}
	_, err = svc.GroupPage(context.Background(), "platform", Filter{}, 20)
	if err == nil || !strings.Contains(err.Error(), "rejected the token") {
		t.Fatalf("want token error for group, got %v", err)
	}
}

func TestUnknownProjectIsNotFound(t *testing.T) {
	svc, _ := setup(t, "good-token")
	_, err := svc.ProjectPage(context.Background(), "secret/vault", Filter{}, "")
	var ge *gitlab.Error
	if err == nil || !asGitlabErr(err, &ge) || ge.Status != 404 {
		t.Fatalf("hidden project should be 404, got %v", err)
	}
}

func TestResultsAreCached(t *testing.T) {
	svc, fake := setup(t, "good-token")
	for range 3 {
		if _, err := svc.ProjectPage(context.Background(), "platform/web", Filter{}, ""); err != nil {
			t.Fatal(err)
		}
	}
	if n := fake.CallCount("gql:ProjectPipelines"); n != 1 {
		t.Errorf("want 1 upstream call thanks to the cache, got %d", n)
	}
}

func TestScopes(t *testing.T) {
	svc, _ := setup(t, "good-token")
	items, err := svc.Scopes(context.Background(), "infra")
	if err != nil {
		t.Fatal(err)
	}
	var kinds []string
	for _, it := range items {
		kinds = append(kinds, it.Kind+":"+it.Path)
	}
	want := "group:platform/infra,project:platform/infra/terraform"
	if strings.Join(kinds, ",") != want {
		t.Errorf("scopes = %v, want %s", kinds, want)
	}
}

func asGitlabErr(err error, target **gitlab.Error) bool {
	ge, ok := err.(*gitlab.Error)
	if ok {
		*target = ge
	}
	return ok
}
