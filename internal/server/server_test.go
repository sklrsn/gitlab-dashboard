package server

import (
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"testing/fstest"
	"time"

	"gitlab-dashboard/internal/config"
	"gitlab-dashboard/internal/dashboard"
	"gitlab-dashboard/internal/fakegitlab"
	"gitlab-dashboard/internal/gitlab"
)

func newTestServer(t *testing.T) *httptest.Server {
	t.Helper()
	fake := httptest.NewServer(fakegitlab.Demo("tok", time.Now()))
	t.Cleanup(fake.Close)
	u, _ := url.Parse(fake.URL)
	cfg := &config.Config{GitLabURL: u, GitLabToken: "tok", DefaultScope: &config.Scope{Kind: "group", Path: "platform"}}
	gl, _ := gitlab.New(gitlab.Options{BaseURL: u, Token: "tok", Timeout: 5 * time.Second})
	ui := fstest.MapFS{
		"index.html":    {Data: []byte("<html>app</html>")},
		"assets/app.js": {Data: []byte("console.log(1)")},
	}
	s, err := New(cfg, dashboard.NewService(gl, time.Second, 50), ui, slog.New(slog.NewTextHandler(io.Discard, nil)))
	if err != nil {
		t.Fatal(err)
	}
	ts := httptest.NewServer(s.Handler())
	t.Cleanup(ts.Close)
	return ts
}

func get(t *testing.T, ts *httptest.Server, path string) (*http.Response, string) {
	t.Helper()
	resp, err := http.Get(ts.URL + path)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	b, _ := io.ReadAll(resp.Body)
	return resp, string(b)
}

func TestConfigEndpoint(t *testing.T) {
	ts := newTestServer(t)
	resp, body := get(t, ts, "/api/config")
	if resp.StatusCode != 200 {
		t.Fatal(resp.Status, body)
	}
	var c map[string]any
	json.Unmarshal([]byte(body), &c)
	if c["title"] != nil || c["hasLogo"] != false || c["defaultScope"].(map[string]any)["path"] != "platform" {
		t.Errorf("config = %v", c)
	}
	if strings.Contains(body, "tok") {
		t.Error("config must never expose the token")
	}
}

func TestPipelinesEndpoint(t *testing.T) {
	ts := newTestServer(t)
	resp, body := get(t, ts, "/api/pipelines?project=platform/api&status=failed")
	if resp.StatusCode != 200 || !strings.Contains(body, `"id":48213`) {
		t.Fatalf("%s %s", resp.Status, body)
	}
	resp, _ = get(t, ts, "/api/pipelines?group=platform")
	if resp.StatusCode != 200 {
		t.Fatal(resp.Status)
	}
}

func TestChildrenEndpoint(t *testing.T) {
	ts := newTestServer(t)
	_, list := get(t, ts, "/api/pipelines?project=platform/api")
	var pg dashboard.Page
	json.Unmarshal([]byte(list), &pg)
	p := pg.Pipelines[0]
	resp, body := get(t, ts, "/api/children?project=platform/api&iid="+p.IID+"&id=48213")
	if resp.StatusCode != 200 || !strings.Contains(body, "deploy-production") {
		t.Fatalf("%s %s", resp.Status, body)
	}
}

func TestBadInputRejected(t *testing.T) {
	ts := newTestServer(t)
	for _, path := range []string{
		"/api/pipelines",
		"/api/pipelines?project=../etc/passwd",
		"/api/pipelines?group=a%20b",
		"/api/pipelines?project=platform/api&status=nope",
		"/api/children?project=platform/api&iid=x&id=1",
		"/api/children?project=platform/api&iid=1&id=-4",
	} {
		resp, body := get(t, ts, path)
		if resp.StatusCode != 400 || !strings.Contains(body, `"error"`) {
			t.Errorf("%s: %s %s", path, resp.Status, body)
		}
	}
	resp, _ := get(t, ts, "/api/nope")
	if resp.StatusCode != 404 {
		t.Errorf("unknown API path: %s", resp.Status)
	}
}

func TestOnlyReadMethods(t *testing.T) {
	ts := newTestServer(t)
	for _, path := range []string{"/api/pipelines?project=platform/api", "/"} {
		resp, err := http.Post(ts.URL+path, "application/json", strings.NewReader("{}"))
		if err != nil {
			t.Fatal(err)
		}
		resp.Body.Close()
		if resp.StatusCode != http.StatusMethodNotAllowed {
			t.Errorf("POST %s = %s, want 405", path, resp.Status)
		}
	}
}

func TestUIAndHeaders(t *testing.T) {
	ts := newTestServer(t)
	resp, body := get(t, ts, "/some/deep/link")
	if resp.StatusCode != 200 || body != "<html>app</html>" {
		t.Errorf("SPA fallback: %s %q", resp.Status, body)
	}
	if csp := resp.Header.Get("Content-Security-Policy"); !strings.Contains(csp, "default-src 'self'") || !strings.Contains(csp, "frame-ancestors 'none'") {
		t.Errorf("CSP = %q", csp)
	}
	if resp.Header.Get("X-Content-Type-Options") != "nosniff" {
		t.Error("missing nosniff")
	}
	resp, _ = get(t, ts, "/assets/app.js")
	if !strings.Contains(resp.Header.Get("Cache-Control"), "immutable") {
		t.Errorf("asset cache header = %q", resp.Header.Get("Cache-Control"))
	}
	resp, _ = get(t, ts, "/assets/missing.js")
	if resp.StatusCode != 404 {
		t.Errorf("missing asset should 404, got %s", resp.Status)
	}
	resp, _ = get(t, ts, "/healthz")
	if resp.StatusCode != 200 {
		t.Error("healthz")
	}
}

func TestGroupedEndpoints(t *testing.T) {
	ts := newTestServer(t)
	for path, want := range map[string]int{
		"/api/groups?project=platform/api&by=name":                      200,
		"/api/groups?group=platform&by=project&window=7d":               200,
		"/api/groups?project=platform/api&by=child&window=24h&open=c:x": 200,
		"/api/groups?project=platform/api&by=project":                   400,
		"/api/groups?project=platform/api&by=bogus":                     400,
		"/api/groups?project=platform/api&by=name&window=1y":            400,
		"/api/groups?by=name":                                           400,
		"/api/scope?group=platform":                                     200,
		"/api/scope?project=secret/vault":                               404,
		"/api/scope?project=../etc":                                     400,
		"/api/runs?project=platform/api&name=Nightly%20release&page=1":  200,
		"/api/runs?project=platform/api&name=Nightly%20release&page=0":  400,
		"/api/runs?project=platform/api&page=1":                         400,
	} {
		if resp, body := get(t, ts, path); resp.StatusCode != want {
			t.Errorf("%s: got %d want %d: %s", path, resp.StatusCode, want, body)
		}
	}
	_, body := get(t, ts, "/api/scope?project=platform/api")
	var item map[string]any
	json.Unmarshal([]byte(body), &item)
	if item["name"] != "Platform / Api" || item["kind"] != "project" {
		t.Errorf("scope: %s", body)
	}
}
