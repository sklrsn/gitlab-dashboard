package dashboard

import (
	"context"
	"strconv"
	"testing"
	"time"

	"gitlab-dashboard/internal/gitlab"
)

func groupsByLabel(gs []PipelineGroup) map[string]PipelineGroup {
	out := map[string]PipelineGroup{}
	for _, g := range gs {
		out[g.Label+"|"+g.Sub] = g
	}
	return out
}

func TestGroupByName(t *testing.T) {
	svc, _ := setup(t, "good-token")
	w, _ := ParseWindow("100")
	pg, err := svc.GroupedPage(context.Background(), "project", "platform/api", "name", w, Filter{}, map[string]bool{"n:Nightly release": true})
	if err != nil {
		t.Fatal(err)
	}
	if pg.Pipelines != 48 || pg.Capped {
		t.Errorf("want 48 pipelines in window, uncapped; got %d capped=%v", pg.Pipelines, pg.Capped)
	}
	g := groupsByLabel(pg.Groups)
	n := g["Nightly release|"]
	if n.Runs != 8 || n.Passed != 5 || n.Failed != 3 || n.Status != "failed" || n.AvgDuration == nil {
		t.Errorf("nightly group: %+v", n)
	}
	if pg.Groups[0].Key != "n:Nightly release" {
		t.Errorf("group with a failed latest run should sort first, got %q", pg.Groups[0].Key)
	}
	if len(n.History) != 8 || n.History[7].ID != 48213 || n.History[0].ID != 48070 {
		t.Errorf("history should run oldest to newest: %+v", n.History)
	}
	if n.OlderProject != "platform/api" || n.OlderName != "Nightly release" {
		t.Errorf("single-project named group should allow older runs: %+v", n)
	}
	if len(n.Items) != 8 || n.Items[0].ID != 48213 {
		t.Errorf("open group should carry its runs newest first, got %d", len(n.Items))
	}
	u := g["(unnamed) · push|"]
	if u.Runs != 40 || u.Items != nil || u.OlderName != "" {
		t.Errorf("unnamed push group: runs=%d items=%d older=%q", u.Runs, len(u.Items), u.OlderName)
	}
}

func TestGroupByChild(t *testing.T) {
	svc, _ := setup(t, "good-token")
	w, _ := ParseWindow("100")
	pg, err := svc.GroupedPage(context.Background(), "project", "platform/api", "child", w, Filter{}, map[string]bool{"c:e2e-shard-1": true})
	if err != nil {
		t.Fatal(err)
	}
	g := groupsByLabel(pg.Groups)
	if it := g["Integration tests|"]; it.Runs != 8 || it.Failed != 3 || it.Status != "failed" {
		t.Errorf("integration tests across parents: %+v", it)
	}
	if b := g["build-images|"]; b.Runs != 8 || b.Passed != 8 {
		t.Errorf("build-images: %+v", b)
	}
	if d := g["Deploy staging|platform/deploy"]; d.Runs != 1 {
		t.Errorf("cross-project child should be its own group with the project as sub: %+v", pg.Groups)
	}
	if d := g["deploy-production|"]; !d.Limited || d.Status != "skipped" {
		t.Errorf("unstarted trigger: %+v", d)
	}
	e := g["e2e-shard-1|"]
	if e.Runs != 1 || len(e.Items) != 1 || e.Items[0].Parent == nil || e.Items[0].Parent.ID != 48215 {
		t.Errorf("grandchild should point at its parent: %+v", e)
	}
}

func TestGroupWindowSpan(t *testing.T) {
	svc, _ := setup(t, "good-token")
	w, _ := ParseWindow("24h")
	pg, err := svc.GroupedPage(context.Background(), "project", "platform/api", "name", w, Filter{}, nil)
	if err != nil {
		t.Fatal(err)
	}
	g := groupsByLabel(pg.Groups)
	if n := g["Nightly release|"]; n.Runs != 2 {
		t.Errorf("24h window should hold only today's 2 nightlies, got %d", n.Runs)
	}
	if pg.Since == nil || time.Since(*pg.Since) > 24*time.Hour {
		t.Errorf("since = %v", pg.Since)
	}
}

func TestGroupByProjectInGroup(t *testing.T) {
	svc, _ := setup(t, "good-token")
	w, _ := ParseWindow("7d")
	pg, err := svc.GroupedPage(context.Background(), "group", "platform", "project", w, Filter{}, nil)
	if err != nil {
		t.Fatal(err)
	}
	g := groupsByLabel(pg.Groups)
	if len(pg.Groups) != 5 || g["platform/api|"].Runs != 47 {
		t.Errorf("want 5 project groups, api with 47 runs (oldest nightly is 8 days old): %+v", g["platform/api|"])
	}
	if _, nested := g["platform/deploy|"]; !nested || g["platform/deploy|"].Runs != 1 {
		t.Errorf("deploy downstream of an in-group pipeline should not count: %+v", g["platform/deploy|"])
	}
}

func TestFinishWindowCapsToCommonPeriod(t *testing.T) {
	svc, _ := setup(t, "good-token")
	at := func(id int64, h int) gitlab.Pipeline {
		ts := time.Now().Add(-time.Duration(h) * time.Hour)
		return gitlab.Pipeline{ID: "gid://gitlab/Ci::Pipeline/" + strconv.FormatInt(id, 10), CreatedAt: &ts}
	}
	// Project A stopped early at 10h ago; project B reached its end.
	a := []gitlab.Pipeline{at(10, 1), at(9, 5), at(8, 10)}
	b := []gitlab.Pipeline{at(7, 2), at(6, 20), at(5, 40)}
	w := finishWindow(Window{Span: 7 * 24 * time.Hour}, [][]gitlab.Pipeline{a, b}, []bool{false, true}, svc)
	if !w.capped || len(w.runs) != 4 {
		t.Fatalf("want 4 runs from the common period, capped; got %d capped=%v", len(w.runs), w.capped)
	}
	if got := ids(w.runs); got[0] != 10 || got[3] != 7 {
		t.Errorf("want [10 9 8 7], got %v", got)
	}
}

func TestOlderRuns(t *testing.T) {
	svc, _ := setup(t, "good-token")
	pg, err := svc.OlderRuns(context.Background(), "platform/api", "Nightly release", 1)
	if err != nil {
		t.Fatal(err)
	}
	if len(pg.Pipelines) != 8 || pg.HasMore || pg.Pipelines[0].ID != 48213 || pg.Pipelines[0].StatusLabel != "failed" {
		t.Errorf("older runs: %d hasMore=%v %+v", len(pg.Pipelines), pg.HasMore, pg.Pipelines[0])
	}
}

func TestScopeName(t *testing.T) {
	svc, _ := setup(t, "good-token")
	g, err := svc.Scope(context.Background(), "group", "platform/infra")
	if err != nil || g.Name != "Platform / Infra" || g.WebURL == "" {
		t.Errorf("group scope: %+v %v", g, err)
	}
	p, err := svc.Scope(context.Background(), "project", "platform/api")
	if err != nil || p.Name != "Platform / Api" {
		t.Errorf("project scope: %+v %v", p, err)
	}
	if _, err := svc.Scope(context.Background(), "project", "secret/vault"); err == nil {
		t.Error("hidden project should fail")
	}
}

func TestOpenGroupSplitsByBranch(t *testing.T) {
	svc, _ := setup(t, "good-token")
	w, _ := ParseWindow("100")
	sub := "u:push" + BranchSep + "r:feat/auth"
	pg, err := svc.GroupedPage(context.Background(), "group", "platform", "name", w, Filter{}, map[string]bool{"u:push": true, sub: true})
	if err != nil {
		t.Fatal(err)
	}
	u := groupsByLabel(pg.Groups)["(unnamed) · push|"]
	if len(u.Branches) < 2 || u.Items != nil {
		t.Fatalf("multi-branch group should carry branches, not items: %d branches, %d items", len(u.Branches), len(u.Items))
	}
	total := 0
	var feat PipelineGroup
	for _, b := range u.Branches {
		total += b.Runs
		if b.Key == sub {
			feat = b
		} else if b.Items != nil {
			t.Errorf("closed branch %q should not carry runs", b.Label)
		}
		if b.Branches != nil || b.OlderName != "" {
			t.Errorf("branch rows have no further level or older runs: %+v", b)
		}
	}
	if total != u.Runs {
		t.Errorf("branch runs add up to %d, group has %d", total, u.Runs)
	}
	if feat.Label != "feat/auth" || len(feat.Items) != 1 || feat.Items[0].ID != 48219 {
		t.Errorf("open branch should carry its runs: %+v", feat)
	}
	if u.Branches[0].Label != "renovate/go" {
		t.Errorf("most recently active branch first (no failures on top), got %q", u.Branches[0].Label)
	}

	// One branch only: runs directly. Grouping by branch: never a second level.
	n := groupsByLabel(pg.Groups)["Nightly release|"]
	if n.Branches != nil {
		t.Error("closed group should not be split")
	}
	byRef, err := svc.GroupedPage(context.Background(), "group", "platform", "ref", w, Filter{}, map[string]bool{"r:main": true})
	if err != nil {
		t.Fatal(err)
	}
	if m := groupsByLabel(byRef.Groups)["main|"]; m.Branches != nil || len(m.Items) == 0 {
		t.Errorf("group by branch has no sub-groups: %d branches, %d items", len(m.Branches), len(m.Items))
	}
	one, _ := svc.GroupedPage(context.Background(), "group", "platform", "name", w, Filter{}, map[string]bool{"n:Nightly release": true})
	if n := groupsByLabel(one.Groups)["Nightly release|"]; n.Branches != nil || len(n.Items) != 8 {
		t.Errorf("single-branch group should open straight to runs: %d branches, %d items", len(n.Branches), len(n.Items))
	}
}
