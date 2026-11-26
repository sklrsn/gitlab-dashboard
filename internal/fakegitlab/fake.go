// Package fakegitlab is an in-memory imitation of the GitLab APIs the
// dashboard uses. It backs the tests and `go run ./cmd/fakegitlab` for UI work.
package fakegitlab

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"
)

type Stage struct{ Name, Status string }

type Bridge struct {
	ID         int64
	Name       string
	Status     string
	Downstream int64 // 0 = not started
}

type Pipeline struct {
	ID       int64
	IID      int
	Project  string
	Name     string
	Status   string // lower-case GitLab status, e.g. "success"
	Source   string
	Ref      string
	SHA      string
	Created  time.Time
	Started  time.Time
	Duration int // seconds; 0 while running
	User     string
	Commit   string
	Upstream int64
	Stages   []Stage
	Bridges  []Bridge
}

// MR is a merge request the fake knows, keyed by project in Server.MRs.
type MR struct {
	IID            int64
	Title          string
	Source, Target string
}

type Server struct {
	Token string
	MRs   map[string][]MR
	// Hidden projects exist but the token can't read them (GraphQL omits them).
	Hidden map[string]bool

	mu        sync.Mutex
	pipelines map[int64]*Pipeline
	projects  []string
	groups    []string
	Calls     map[string]int
}

func New(token string) *Server {
	return &Server{Token: token, MRs: map[string][]MR{}, Hidden: map[string]bool{}, pipelines: map[int64]*Pipeline{}, Calls: map[string]int{}}
}

func (s *Server) AddProject(path string) { s.projects = append(s.projects, path) }
func (s *Server) AddGroup(path string)   { s.groups = append(s.groups, path) }
func (s *Server) Add(p *Pipeline) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.pipelines[p.ID] = p
}

func (s *Server) CallCount(op string) int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.Calls[op]
}

func (s *Server) count(op string) {
	s.mu.Lock()
	s.Calls[op]++
	s.mu.Unlock()
}

func (s *Server) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if r.Header.Get("Authorization") != "Bearer "+s.Token {
		w.WriteHeader(http.StatusUnauthorized)
		w.Write([]byte(`{"message":"401 Unauthorized"}`))
		return
	}
	if r.Method == http.MethodPost && r.URL.Path == "/api/graphql" {
		s.graphql(w, r)
		return
	}
	if r.Method != http.MethodGet || !strings.HasPrefix(r.URL.EscapedPath(), "/api/v4/") {
		http.NotFound(w, r)
		return
	}
	parts := strings.Split(strings.TrimPrefix(r.URL.EscapedPath(), "/api/v4/"), "/")
	for i := range parts {
		parts[i], _ = url.PathUnescape(parts[i])
	}
	q := r.URL.Query()
	switch {
	case len(parts) == 1 && parts[0] == "groups":
		s.count("rest:groups")
		var out []map[string]any
		for _, g := range s.groups {
			if strings.Contains(g, q.Get("search")) {
				out = append(out, map[string]any{"id": len(out) + 1, "full_path": g, "full_name": strings.ReplaceAll(g, "/", " / "), "web_url": "http://gitlab.test/groups/" + g})
			}
		}
		writeJSON(w, out)
	case len(parts) == 1 && parts[0] == "projects":
		s.count("rest:projects")
		var out []map[string]any
		for _, p := range s.projects {
			if !s.Hidden[p] && strings.Contains(p, q.Get("search")) {
				out = append(out, project(p, len(out)+1))
			}
		}
		writeJSON(w, out)
	case len(parts) == 2 && parts[0] == "groups":
		s.count("rest:group")
		if !contains(s.groups, parts[1]) {
			http.Error(w, `{"message":"404 Group Not Found"}`, http.StatusNotFound)
			return
		}
		writeJSON(w, map[string]any{"id": 1, "full_path": parts[1], "full_name": titleName(parts[1]), "web_url": "http://gitlab.test/groups/" + parts[1]})
	case len(parts) == 2 && parts[0] == "projects":
		s.count("rest:project")
		if s.Hidden[parts[1]] || !contains(s.projects, parts[1]) {
			http.Error(w, `{"message":"404 Project Not Found"}`, http.StatusNotFound)
			return
		}
		m := project(parts[1], 1)
		m["name_with_namespace"] = titleName(parts[1])
		writeJSON(w, m)
	case len(parts) == 3 && parts[0] == "projects" && parts[2] == "pipelines":
		s.count("rest:pipelines")
		if s.Hidden[parts[1]] || !contains(s.projects, parts[1]) {
			http.Error(w, `{"message":"404 Project Not Found"}`, http.StatusNotFound)
			return
		}
		page, _ := strconv.Atoi(q.Get("page"))
		per, _ := strconv.Atoi(q.Get("per_page"))
		page, per = max(page, 1), max(per, 20)
		var list []*Pipeline
		s.mu.Lock()
		for _, p := range s.pipelines {
			if p.Project == parts[1] && (q.Get("name") == "" || p.Name == q.Get("name")) {
				list = append(list, p)
			}
		}
		s.mu.Unlock()
		sort.Slice(list, func(i, j int) bool { return list[i].ID > list[j].ID })
		out := []map[string]any{}
		for _, p := range list[min((page-1)*per, len(list)):min(page*per, len(list))] {
			out = append(out, map[string]any{"id": p.ID, "iid": p.IID, "project_id": 1, "name": nilIfEmpty(p.Name), "status": p.Status,
				"source": p.Source, "ref": p.Ref, "sha": p.SHA, "created_at": p.Created,
				"web_url": fmt.Sprintf("http://gitlab.test/%s/-/pipelines/%d", p.Project, p.ID)})
		}
		writeJSON(w, out)
	case len(parts) == 3 && parts[0] == "projects" && parts[2] == "merge_requests":
		s.count("rest:mergeRequests")
		want := map[string]bool{}
		for _, v := range q["iids[]"] {
			want[v] = true
		}
		out := []map[string]any{}
		for _, mr := range s.MRs[parts[1]] {
			if want[strconv.FormatInt(mr.IID, 10)] {
				out = append(out, map[string]any{"id": 5000 + mr.IID, "iid": mr.IID, "title": mr.Title, "source_branch": mr.Source,
					"target_branch": mr.Target, "web_url": fmt.Sprintf("http://gitlab.test/%s/-/merge_requests/%d", parts[1], mr.IID)})
			}
		}
		writeJSON(w, out)
	case len(parts) == 3 && parts[0] == "groups" && parts[2] == "projects":
		s.count("rest:groupProjects")
		limit, _ := strconv.Atoi(q.Get("per_page"))
		var out []map[string]any
		for _, p := range s.projects {
			if strings.HasPrefix(p, parts[1]+"/") && !s.Hidden[p] && (limit == 0 || len(out) < limit) {
				out = append(out, project(p, len(out)+1))
			}
		}
		if out == nil && !contains(s.groups, parts[1]) {
			http.Error(w, `{"message":"404 Group Not Found"}`, http.StatusNotFound)
			return
		}
		writeJSON(w, out)
	case len(parts) == 5 && parts[0] == "projects" && parts[2] == "pipelines" && parts[4] == "bridges":
		s.count("rest:bridges")
		id, _ := strconv.ParseInt(parts[3], 10, 64)
		s.mu.Lock()
		p := s.pipelines[id]
		s.mu.Unlock()
		if p == nil || p.Project != parts[1] {
			http.Error(w, `{"message":"404 Not found"}`, http.StatusNotFound)
			return
		}
		out := []map[string]any{}
		for _, b := range p.Bridges {
			m := map[string]any{"id": b.ID, "name": b.Name, "stage": "trigger", "status": b.Status, "web_url": fmt.Sprintf("http://gitlab.test/%s/-/jobs/%d", p.Project, b.ID), "downstream_pipeline": nil}
			s.mu.Lock()
			d := s.pipelines[b.Downstream]
			s.mu.Unlock()
			if d != nil {
				m["downstream_pipeline"] = map[string]any{
					"id": d.ID, "project_id": 99, "ref": d.Ref, "sha": d.SHA, "status": d.Status,
					"web_url": fmt.Sprintf("http://gitlab.test/%s/-/pipelines/%d", d.Project, d.ID), "created_at": d.Created,
				}
			}
			out = append(out, m)
		}
		writeJSON(w, out)
	default:
		http.NotFound(w, r)
	}
}

func project(path string, id int) map[string]any {
	return map[string]any{"id": id, "path_with_namespace": path, "name_with_namespace": strings.ReplaceAll(path, "/", " / "), "web_url": "http://gitlab.test/" + path}
}

func (s *Server) graphql(w http.ResponseWriter, r *http.Request) {
	var req struct {
		OperationName string         `json:"operationName"`
		Query         string         `json:"query"`
		Variables     map[string]any `json:"variables"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "bad json", http.StatusBadRequest)
		return
	}
	s.count("gql:" + req.OperationName)
	str := func(k string) string { v, _ := req.Variables[k].(string); return v }
	path := str("fullPath")
	if s.Hidden[path] || !contains(s.projects, path) {
		writeJSON(w, map[string]any{"data": map[string]any{"project": nil}})
		return
	}
	switch req.OperationName {
	case "ProjectPipelines":
		first := int(req.Variables["first"].(float64))
		offset, _ := strconv.Atoi(str("after"))
		var list []*Pipeline
		s.mu.Lock()
		for _, p := range s.pipelines {
			if p.Project == path && (str("ref") == "" || p.Ref == str("ref")) && (str("status") == "" || strings.ToUpper(p.Status) == str("status")) {
				list = append(list, p)
			}
		}
		s.mu.Unlock()
		sort.Slice(list, func(i, j int) bool { return list[i].ID > list[j].ID })
		end := min(offset+first, len(list))
		nodes := []any{}
		for _, p := range list[min(offset, len(list)):end] {
			nodes = append(nodes, s.node(p))
		}
		writeJSON(w, map[string]any{"data": map[string]any{"project": map[string]any{"pipelines": map[string]any{
			"pageInfo": map[string]any{"hasNextPage": end < len(list), "endCursor": strconv.Itoa(end)},
			"nodes":    nodes,
		}}}})
	case "PipelineDownstream":
		iid, _ := strconv.Atoi(str("iid"))
		parent := s.find(func(p *Pipeline) bool { return p.Project == path && p.IID == iid })
		if parent == nil {
			writeJSON(w, map[string]any{"data": map[string]any{"project": map[string]any{"pipeline": nil}}})
			return
		}
		nodes := []any{}
		for _, d := range s.downstream(parent.ID) {
			if !s.Hidden[d.Project] {
				nodes = append(nodes, s.node(d))
			}
		}
		writeJSON(w, map[string]any{"data": map[string]any{"project": map[string]any{"pipeline": map[string]any{"downstream": map[string]any{"nodes": nodes}}}}})
	default:
		writeJSON(w, map[string]any{"errors": []any{map[string]any{"message": "unknown operation " + req.OperationName}}})
	}
}

func (s *Server) find(fn func(*Pipeline) bool) *Pipeline {
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, p := range s.pipelines {
		if fn(p) {
			return p
		}
	}
	return nil
}

func (s *Server) downstream(id int64) []*Pipeline {
	s.mu.Lock()
	defer s.mu.Unlock()
	var out []*Pipeline
	for _, p := range s.pipelines {
		if p.Upstream == id {
			out = append(out, p)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].ID < out[j].ID })
	return out
}

var labels = map[string]string{"success": "passed"}

func label(status string) string {
	if l, ok := labels[status]; ok {
		return l
	}
	return status
}

func (s *Server) node(p *Pipeline) map[string]any {
	stages := []any{}
	for _, st := range p.Stages {
		stages = append(stages, map[string]any{"name": st.Name, "detailedStatus": map[string]any{"group": st.Status, "label": label(st.Status)}})
	}
	var dur any
	if p.Duration > 0 {
		dur = p.Duration
	}
	var finished any
	if p.Duration > 0 {
		finished = p.Started.Add(time.Duration(p.Duration) * time.Second)
	}
	var name, started any
	if p.Name != "" {
		name = p.Name
	}
	if !p.Started.IsZero() {
		started = p.Started
	}
	n := map[string]any{
		"id": fmt.Sprintf("gid://gitlab/Ci::Pipeline/%d", p.ID), "iid": strconv.Itoa(p.IID), "name": name,
		"status": strings.ToUpper(p.Status), "source": p.Source, "ref": p.Ref, "sha": p.SHA,
		"path":      fmt.Sprintf("/%s/-/pipelines/%d", p.Project, p.ID),
		"createdAt": p.Created, "startedAt": started, "finishedAt": finished, "duration": dur,
		"detailedStatus": map[string]any{"group": p.Status, "label": label(p.Status)},
		"user":           map[string]any{"username": p.User, "name": strings.ToUpper(p.User[:1]) + p.User[1:], "avatarUrl": nil},
		"commit":         map[string]any{"title": p.Commit},
		"project":        map[string]any{"fullPath": p.Project, "name": p.Project[strings.LastIndex(p.Project, "/")+1:], "webUrl": "http://gitlab.test/" + p.Project},
		"upstream":       nil,
		"downstream":     map[string]any{"count": len(s.downstream(p.ID))},
		"stages":         map[string]any{"nodes": stages},
	}
	if up := s.get(p.Upstream); up != nil {
		n["upstream"] = map[string]any{"id": fmt.Sprintf("gid://gitlab/Ci::Pipeline/%d", up.ID), "path": fmt.Sprintf("/%s/-/pipelines/%d", up.Project, up.ID), "project": map[string]any{"fullPath": up.Project}}
	}
	return n
}

func (s *Server) get(id int64) *Pipeline {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.pipelines[id]
}

// titleName turns "platform/infra" into "Platform / Infra", like GitLab display names.
func titleName(path string) string {
	segs := strings.Split(path, "/")
	for i, s := range segs {
		segs[i] = strings.ToUpper(s[:1]) + s[1:]
	}
	return strings.Join(segs, " / ")
}

func nilIfEmpty(s string) any {
	if s == "" {
		return nil
	}
	return s
}

func contains(list []string, v string) bool {
	for _, x := range list {
		if x == v {
			return true
		}
	}
	return false
}

func writeJSON(w http.ResponseWriter, v any) {
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(v)
}
