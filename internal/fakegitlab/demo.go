package fakegitlab

import (
	"fmt"
	"time"
)

// Demo returns a server seeded with a realistic group of projects, including
// nested child pipelines, multi-project downstreams, and a hidden project.
func Demo(token string, now time.Time) *Server {
	s := New(token)
	for _, g := range []string{"platform", "platform/infra", "secret"} {
		s.AddGroup(g)
	}
	for _, p := range []string{"platform/api", "platform/web", "platform/cli", "platform/deploy", "platform/infra/terraform", "secret/vault"} {
		s.AddProject(p)
	}
	s.Hidden["secret/vault"] = true

	ago := func(d time.Duration) time.Time { return now.Add(-d) }
	ok := []Stage{{"build", "success"}, {"test", "success"}, {"package", "success"}}
	sha := func(id int64) string { return fmt.Sprintf("%08x%032x", id*2654435761%0xffffffff, id) }
	iid := 900
	add := func(p *Pipeline) *Pipeline {
		iid++
		p.IID = iid
		if p.SHA == "" {
			p.SHA = sha(p.ID)
		}
		if p.User == "" {
			p.User = "release-bot"
		}
		if p.Source == "" {
			p.Source = "push"
		}
		s.Add(p)
		return p
	}

	// Older history for pagination.
	for i := range 40 {
		id := int64(48100 + i)
		st := "success"
		if i%4 == 1 {
			st = "failed"
		}
		add(&Pipeline{ID: id, Project: "platform/api", Status: st, Ref: "main", Created: ago(time.Duration(60-i) * time.Hour),
			Started: ago(time.Duration(60-i) * time.Hour), Duration: 400 + i*13, User: "alice", Commit: fmt.Sprintf("Refactor handler %d", i),
			Stages: []Stage{{"build", "success"}, {"test", st}}})
	}

	// A finished nightly with three children, one in another project.
	add(&Pipeline{ID: 48201, Project: "platform/api", Name: "Nightly release", Source: "schedule", Status: "success", Ref: "main",
		Created: ago(3 * time.Hour), Started: ago(3 * time.Hour), Duration: 1260, Commit: "Bump version to 2.8.0", Stages: ok,
		Bridges: []Bridge{{701, "build-images", "success", 48202}, {702, "integration-tests", "success", 48203}, {703, "deploy-staging", "success", 48204}}})
	add(&Pipeline{ID: 48202, Project: "platform/api", Source: "parent_pipeline", Upstream: 48201, Status: "success", Ref: "main",
		Created: ago(3 * time.Hour), Started: ago(3 * time.Hour), Duration: 250, Commit: "Bump version to 2.8.0", Stages: []Stage{{"build", "success"}, {"push", "success"}}})
	add(&Pipeline{ID: 48203, Project: "platform/api", Source: "parent_pipeline", Upstream: 48201, Status: "success", Ref: "main",
		Created: ago(3 * time.Hour), Started: ago(3 * time.Hour), Duration: 610, Commit: "Bump version to 2.8.0", Stages: []Stage{{"test", "success"}}})
	add(&Pipeline{ID: 48204, Project: "platform/deploy", Name: "Deploy staging", Source: "pipeline", Upstream: 48201, Status: "success", Ref: "main",
		Created: ago(150 * time.Minute), Started: ago(150 * time.Minute), Duration: 190, Commit: "Deploy api 2.8.0", Stages: []Stage{{"deploy", "success"}, {"verify", "success"}}})

	add(&Pipeline{ID: 48205, Project: "platform/cli", Status: "canceled", Ref: "v1.4.0", Created: ago(2 * time.Hour), Started: ago(2 * time.Hour),
		Duration: 45, User: "bob", Commit: "Release v1.4.0", Stages: []Stage{{"build", "success"}, {"release", "canceled"}}})
	add(&Pipeline{ID: 48210, Project: "platform/web", Status: "success-with-warnings", Ref: "main", Created: ago(70 * time.Minute),
		Started: ago(70 * time.Minute), Duration: 512, User: "carol", Commit: "Update landing page copy", Stages: []Stage{{"build", "success"}, {"lint", "success-with-warnings"}, {"test", "success"}}})
	add(&Pipeline{ID: 48212, Project: "platform/deploy", Name: "Rotate certificates", Status: "success", Ref: "main", Created: ago(40 * time.Minute),
		Started: ago(40 * time.Minute), Duration: 88, User: "dave", Commit: "Rotate certs", Stages: []Stage{{"rotate", "success"}}})

	// A failed nightly: nested children, a skipped trigger, and a child the token can't see.
	add(&Pipeline{ID: 48213, Project: "platform/api", Name: "Nightly release", Source: "schedule", Status: "failed", Ref: "main",
		Created: ago(8 * time.Minute), Started: ago(8 * time.Minute), Duration: 724, Commit: "Add rate limiting to auth endpoints",
		Stages:  []Stage{{"build", "success"}, {"test", "success"}, {"integration", "failed"}, {"deploy", "skipped"}},
		Bridges: []Bridge{{711, "build-images", "success", 48214}, {712, "integration-tests", "failed", 48215}, {713, "vault-sync", "success", 48218}, {714, "deploy-production", "skipped", 0}}})
	add(&Pipeline{ID: 48214, Project: "platform/api", Source: "parent_pipeline", Upstream: 48213, Status: "success", Ref: "main",
		Created: ago(8 * time.Minute), Started: ago(8 * time.Minute), Duration: 250, Commit: "Add rate limiting to auth endpoints", Stages: []Stage{{"build", "success"}, {"push", "success"}}})
	add(&Pipeline{ID: 48215, Project: "platform/api", Name: "Integration tests", Source: "parent_pipeline", Upstream: 48213, Status: "failed", Ref: "main",
		Created: ago(7 * time.Minute), Started: ago(7 * time.Minute), Duration: 411, Commit: "Add rate limiting to auth endpoints", Stages: []Stage{{"setup", "success"}, {"shards", "failed"}},
		Bridges: []Bridge{{721, "e2e-shard-1", "failed", 48216}, {722, "e2e-shard-2", "success", 48217}}})
	add(&Pipeline{ID: 48216, Project: "platform/api", Source: "parent_pipeline", Upstream: 48215, Status: "failed", Ref: "main",
		Created: ago(6 * time.Minute), Started: ago(6 * time.Minute), Duration: 300, Commit: "Add rate limiting to auth endpoints", Stages: []Stage{{"e2e", "failed"}}})
	add(&Pipeline{ID: 48217, Project: "platform/api", Source: "parent_pipeline", Upstream: 48215, Status: "success", Ref: "main",
		Created: ago(6 * time.Minute), Started: ago(6 * time.Minute), Duration: 280, Commit: "Add rate limiting to auth endpoints", Stages: []Stage{{"e2e", "success"}}})
	add(&Pipeline{ID: 48218, Project: "secret/vault", Source: "pipeline", Upstream: 48213, Status: "success", Ref: "main",
		Created: ago(6 * time.Minute), Started: ago(6 * time.Minute), Duration: 60, Commit: "Sync secrets", Stages: []Stage{{"sync", "success"}}})

	// Unnamed running pipeline with a running child.
	add(&Pipeline{ID: 48219, Project: "platform/web", Status: "running", Ref: "feat/auth", Created: ago(3 * time.Minute), Started: ago(3 * time.Minute),
		User: "carol", Commit: "Fix token refresh", Stages: []Stage{{"build", "success"}, {"test", "running"}, {"deploy", "created"}},
		Bridges: []Bridge{{731, "frontend-tests", "running", 48220}}})
	add(&Pipeline{ID: 48220, Project: "platform/web", Source: "parent_pipeline", Upstream: 48219, Status: "running", Ref: "feat/auth",
		Created: ago(2 * time.Minute), Started: ago(2 * time.Minute), Commit: "Fix token refresh", Stages: []Stage{{"unit", "success"}, {"browser", "running"}}})

	add(&Pipeline{ID: 48221, Project: "platform/cli", Status: "pending", Ref: "renovate/go", Created: ago(time.Minute), User: "renovate-bot",
		Commit: "Update Go dependencies", Stages: []Stage{{"build", "created"}, {"test", "created"}}})
	add(&Pipeline{ID: 48222, Project: "platform/infra/terraform", Name: "Terraform plan", Status: "manual", Ref: "main", Created: ago(25 * time.Minute),
		Started: ago(25 * time.Minute), Duration: 96, User: "erin", Commit: "Add NAT gateway", Stages: []Stage{{"plan", "success"}, {"apply", "manual"}}})
	return s
}
