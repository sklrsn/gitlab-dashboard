package dashboard

import (
	"context"
	"strings"
	"testing"
)

func find(ps []Pipeline, id int64) Pipeline {
	for _, p := range ps {
		if p.ID == id {
			return p
		}
	}
	return Pipeline{}
}

func TestMergeRequestPipelinesShowSourceBranch(t *testing.T) {
	svc, fake := setup(t, "good-token")
	ctx := context.Background()
	pg, err := svc.ProjectPage(ctx, "platform/web", Filter{}, "")
	if err != nil {
		t.Fatal(err)
	}
	p := find(pg.Pipelines, 48223)
	mr := p.MergeRequest
	if p.Ref != "refs/merge-requests/42/merge" || p.Branch != "feat/auth" || mr == nil ||
		mr.IID != 42 || mr.SourceBranch != "feat/auth" || mr.TargetBranch != "main" || !strings.HasSuffix(mr.WebURL, "/-/merge_requests/42") {
		t.Errorf("merge request pipeline: branch=%q mr=%+v", p.Branch, mr)
	}
	if push := find(pg.Pipelines, 48219); push.Branch != "feat/auth" || push.MergeRequest != nil {
		t.Errorf("branch pipeline keeps its ref as branch: %+v", push)
	}

	kids, err := svc.Children(ctx, "platform/web", p.IID, p.ID)
	if err != nil || len(kids) != 1 || kids[0].Branch != "feat/auth" || kids[0].MergeRequest == nil {
		t.Errorf("child of a merge request pipeline: %+v %v", kids, err)
	}

	// Cached: a second page doesn't look the merge request up again.
	before := fake.CallCount("rest:mergeRequests")
	if _, err := svc.ProjectPage(ctx, "platform/web", Filter{Status: "success"}, ""); err != nil {
		t.Fatal(err)
	}
	if after := fake.CallCount("rest:mergeRequests"); after != before {
		t.Errorf("merge request lookup not cached: %d → %d calls", before, after)
	}
}

func TestUnknownMergeRequestFallsBackToNumber(t *testing.T) {
	svc, _ := setup(t, "good-token")
	pg, err := svc.ProjectPage(context.Background(), "platform/cli", Filter{}, "")
	if err != nil {
		t.Fatal(err)
	}
	p := find(pg.Pipelines, 48225)
	if p.Branch != "!77" || p.MergeRequest == nil || p.MergeRequest.IID != 77 || !strings.HasSuffix(p.MergeRequest.WebURL, "/platform/cli/-/merge_requests/77") {
		t.Errorf("unknown merge request: branch=%q mr=%+v", p.Branch, p.MergeRequest)
	}
}

func TestBranchGroupsMergeBranchAndMRPipelines(t *testing.T) {
	svc, _ := setup(t, "good-token")
	w, _ := ParseWindow("100")
	pg, err := svc.GroupedPage(context.Background(), "group", "platform", "ref", w, Filter{}, nil)
	if err != nil {
		t.Fatal(err)
	}
	g := groupsByLabel(pg.Groups)
	if f := g["feat/auth|"]; f.Runs != 2 || f.MergeRequest == nil || f.MergeRequest.IID != 42 {
		t.Errorf("feat/auth should hold its push and merge request pipelines: %+v", f)
	}
	if n := g["!77|"]; n.Runs != 1 || n.Status != "failed" {
		t.Errorf("unknown merge request groups under its number: %+v", n)
	}
	for _, grp := range pg.Groups {
		if strings.HasPrefix(grp.Label, "refs/merge-requests/") {
			t.Errorf("raw merge request ref as a group: %q", grp.Label)
		}
	}
}
