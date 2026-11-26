# gitlab-dashboard

A read-only, Blue Ocean–style pipeline dashboard for self-hosted GitLab. It shows top-level pipelines for a group or project. Expanding a pipeline shows the child pipelines and downstream (multi-project) pipelines it started, to any depth.

- **One static binary.** The UI, fonts and icons are embedded. It needs nothing at runtime except network access to GitLab.
- **GitLab is the only data source.** No database (in v1). Answers are reused for a few seconds so many open browsers don't multiply load on GitLab.
- **Read-only.** Every pipeline links to GitLab, where people run, retry or cancel pipelines with their own permissions.

## How the views work

| View | What's listed |
|---|---|
| Project (`?project=group/app`) | Every pipeline in the project except same-project child pipelines (`source=parent_pipeline`), which appear under their parent. Pipelines triggered from another project show a "triggered by" link. |
| Group (`?group=platform`) | Newest pipelines across the group's most recently active projects (subgroups included, `GROUP_FANOUT_LIMIT`, default 50), merged by pipeline ID. Downstream pipelines whose parent is in the same group are nested under that parent rather than listed twice. The group view shows at most 100 rows; pick a single project to page further back. |
| Expanded row | Child pipelines, named by `workflow: name` or else by the trigger job that started them. Also shows trigger jobs that haven't started a pipeline yet (manual/skipped), and a "limited" row for downstream pipelines in projects the token can't read. |

The title bar shows the chosen group or project's GitLab name; click it to switch to another one.

### Group by

The **Group by** dropdown turns the list into one summary row per group: latest status, the last 10 runs as dots (oldest to newest), how many passed, average duration and when it last ran. Groups whose latest run failed come first. Click a group to see its runs; each run still expands into its children. When a group's runs span several branches, it first shows one summary row per branch (collapsed); a group with a single branch opens straight to its runs. Grouping by Branch / tag has no second level.

Merge request pipelines run on `refs/merge-requests/<number>/merge` (or `/head`). The dashboard shows their source branch instead, with a `!<number>` chip linking to the merge request, and groups them with that branch's other pipelines. The source branch comes from one batched lookup per project, cached for 5 minutes; if the merge request can't be read, the pipeline shows `!<number>`. The branch filter still matches GitLab's raw ref, so a branch name finds only that branch's own pipelines.

| Group by | One group per |
|---|---|
| Workflow name | `workflow: name`; unnamed pipelines become "(unnamed) · push", "(unnamed) · schedule", … |
| Child pipeline | child or downstream pipeline name, across every parent; each run links back to its parent |
| Branch / tag | ref |
| Trigger source | push, schedule, merge request, API, … |
| Project | project (group view only) |

Groups are built from a **window**: the last 100 pipelines, the last 24 hours, or the last 7 days. The dashboard reads at most 1,000 pipelines per window (300 per project in a group view) and child pipelines for at most 200 parents; when a window holds more, a banner says which period the groups cover. Workflow groups in a single project can load older runs beyond the window ("Load older runs"), with fewer details per row. Grouped views refresh every 30 s.

Filters (status, branch/tag), the grouping, the window and the chosen scope are kept in the URL, so views can be bookmarked and shared. The list refreshes every 10 s while something is running and every 30 s otherwise, and pauses in background tabs.

## GitLab token

Create a **group access token** (or a token for a bot user) with:

- scope **`read_api`**
- role **Reporter** or higher (Guest can't read jobs in private projects)

It only sees the groups and projects it belongs to. Downstream pipelines in projects outside its reach show as "limited" rows with a link to GitLab. Tokens expire, so put the expiry date in your calendar. When GitLab rejects the token, the dashboard says so on screen and in its log.

## Configuration

All settings are environment variables. See [`deploy/env.example`](deploy/env.example) for the annotated list.

| Variable | Default | Purpose |
|---|---|---|
| `GITLAB_URL` | required | e.g. `https://gitlab.example.com` |
| `GITLAB_TOKEN` / `GITLAB_TOKEN_FILE` | required | token, or a file containing it (preferred) |
| `GITLAB_CA_FILE` | | PEM CA bundle for an internally signed GitLab certificate |
| `LISTEN_ADDR` | `:8080` | listen address |
| `TLS_CERT_FILE`, `TLS_KEY_FILE` | | serve HTTPS directly (no reverse proxy) |
| `LOGO_FILE` | | logo shown left of the group/project name |
| `DEFAULT_SCOPE` | | `group:<path>` or `project:<path>` shown when the URL has none |
| `GROUP_FANOUT_LIMIT` | `50` | projects read for the group view |
| `CACHE_TTL` | `10s` | how long GitLab answers are reused |
| `GITLAB_TIMEOUT` | `20s` | per-request timeout to GitLab |

## Build

Requirements: Go 1.24+ and Node 20+ **on the build machine only**.

```bash
make build                               # bin/gitlab-dashboard-<os>-<arch>
make build GOOS=linux GOARCH=amd64       # cross-compile for a Linux VM
make test                                # go vet, go test -race, TypeScript check
```

The binary is built with `CGO_ENABLED=0` and is fully static. `gitlab-dashboard version` prints the version and commit.

## Deploy on a VM (systemd)

```bash
make build GOOS=linux GOARCH=amd64
scp -r bin/gitlab-dashboard-linux-amd64 deploy vm:/tmp/
ssh vm 'sudo /tmp/deploy/systemd/install.sh /tmp/gitlab-dashboard-linux-amd64'
ssh vm 'sudo vi /etc/gitlab-dashboard/env && sudo systemctl enable --now gitlab-dashboard'
```

- Runs as the unprivileged `gitlab-dashboard` user with systemd sandboxing, and `Restart=always`.
- Config is in `/etc/gitlab-dashboard/env` (mode 0600). Logs: `journalctl -u gitlab-dashboard -f`.
- Without a reverse proxy, set `TLS_CERT_FILE`/`TLS_KEY_FILE` and `LISTEN_ADDR=:443`. The unit grants only the capability to bind low ports.
- Upgrades: run `install.sh` again with the new binary; it restarts the service.

## Deploy with Docker + HAProxy

```bash
cd deploy/docker
cp ../env.example .env                     # set GITLAB_URL; leave GITLAB_TOKEN empty
mkdir -p secrets certs
printf '%s' 'glpat-…' > secrets/gitlab_token && chmod 600 secrets/gitlab_token
cat server.crt server.key > certs/site.pem
docker compose up -d --build
```

HAProxy terminates TLS, redirects HTTP to HTTPS, and health-checks `/healthz`. The app container is read-only, drops all capabilities and runs as non-root on a distroless base image.

## Local development

```bash
make dev-fake      # fake GitLab with demo data on :8929
make dev-server    # dashboard on :8080 using the fake
make dev-ui        # optional: Vite dev server with hot reload on :5173
```

`internal/fakegitlab` imitates the GitLab GraphQL/REST calls the dashboard makes. The Go tests use it too.

## GitLab APIs used

All calls are reads:

- GraphQL `project.pipelines` and `project.pipeline(iid).downstream` for pipeline lists, stages, upstream/downstream links
- REST `GET /projects/:id/pipelines/:id/bridges` to name children by trigger job and find unstarted/hidden ones
- REST `GET /groups/:id/projects`, `GET /groups`, `GET /projects` for the group view and the scope picker
- REST `GET /groups/:id`, `GET /projects/:id` for the title, `GET /projects/:id/pipelines?name=` for older runs of a workflow, and `GET /projects/:id/merge_requests?iids[]=` for the source branch of merge request pipelines

## Roadmap

- **v2:** alert rules (pipeline name / project / branch → email, later Slack/SMS) via GitLab pipeline webhooks; admin-only settings page; encrypted SQLite for settings.
- **v3:** Blue Ocean–style stage/job graph and log viewer.
