// Package server exposes the read-only dashboard API and the embedded UI.
package server

import (
	"encoding/json"
	"errors"
	"io/fs"
	"log/slog"
	"net/http"
	"net/url"
	"os"
	"path"
	"strconv"
	"strings"
	"time"

	"gitlab-dashboard/internal/config"
	"gitlab-dashboard/internal/dashboard"
	"gitlab-dashboard/internal/gitlab"
)

type Server struct {
	cfg  *config.Config
	svc  *dashboard.Service
	ui   fs.FS
	logo []byte
	log  *slog.Logger
}

func New(cfg *config.Config, svc *dashboard.Service, ui fs.FS, log *slog.Logger) (*Server, error) {
	s := &Server{cfg: cfg, svc: svc, ui: ui, log: log}
	if cfg.LogoFile != "" {
		b, err := os.ReadFile(cfg.LogoFile)
		if err != nil {
			return nil, err
		}
		s.logo = b
	}
	return s, nil
}

func (s *Server) Handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /healthz", func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/plain")
		w.Write([]byte("ok\n"))
	})
	mux.HandleFunc("GET /api/config", s.handleConfig)
	mux.HandleFunc("GET /api/scopes", s.handleScopes)
	mux.HandleFunc("GET /api/pipelines", s.handlePipelines)
	mux.HandleFunc("GET /api/children", s.handleChildren)
	mux.HandleFunc("GET /api/scope", s.handleScope)
	mux.HandleFunc("GET /api/groups", s.handleGroups)
	mux.HandleFunc("GET /api/runs", s.handleRuns)
	mux.HandleFunc("GET /logo", s.handleLogo)
	mux.HandleFunc("GET /api/", func(w http.ResponseWriter, _ *http.Request) {
		writeErr(w, http.StatusNotFound, "unknown API endpoint")
	})
	mux.HandleFunc("GET /", s.handleUI)
	return s.logRequests(securityHeaders(mux))
}

type configResp struct {
	HasLogo      bool          `json:"hasLogo"`
	GitLabURL    string        `json:"gitlabUrl"`
	DefaultScope *config.Scope `json:"defaultScope,omitempty"`
}

func (s *Server) handleConfig(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, configResp{
		HasLogo:      s.logo != nil,
		GitLabURL:    s.cfg.GitLabURL.String(),
		DefaultScope: s.cfg.DefaultScope,
	})
}

func (s *Server) handleScopes(w http.ResponseWriter, r *http.Request) {
	q := strings.TrimSpace(r.URL.Query().Get("search"))
	if len(q) > 100 {
		writeErr(w, http.StatusBadRequest, "search is too long")
		return
	}
	items, err := s.svc.Scopes(r.Context(), q)
	if err != nil {
		s.upstreamErr(w, r, err)
		return
	}
	writeJSON(w, items)
}

func (s *Server) handlePipelines(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	f := dashboard.Filter{Ref: strings.TrimSpace(q.Get("ref")), Status: q.Get("status")}
	if len(f.Ref) > 255 {
		writeErr(w, http.StatusBadRequest, "ref is too long")
		return
	}
	group, project := q.Get("group"), q.Get("project")
	var (
		page dashboard.Page
		err  error
	)
	switch {
	case project != "" && config.ValidPath(project):
		page, err = s.svc.ProjectPage(r.Context(), project, f, q.Get("cursor"))
	case group != "" && config.ValidPath(group):
		limit, _ := strconv.Atoi(q.Get("limit"))
		page, err = s.svc.GroupPage(r.Context(), group, f, limit)
	default:
		writeErr(w, http.StatusBadRequest, "pass a valid group or project path")
		return
	}
	if err != nil {
		s.upstreamErr(w, r, err)
		return
	}
	writeJSON(w, page)
}

func (s *Server) handleChildren(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	project, iid := q.Get("project"), q.Get("iid")
	id, err := strconv.ParseInt(q.Get("id"), 10, 64)
	if !config.ValidPath(project) || err != nil || id <= 0 || !isDigits(iid) {
		writeErr(w, http.StatusBadRequest, "pass a valid project, iid and id")
		return
	}
	kids, err := s.svc.Children(r.Context(), project, iid, id)
	if err != nil {
		s.upstreamErr(w, r, err)
		return
	}
	writeJSON(w, kids)
}

// scopeParam reads ?group= or ?project= with a valid path.
func scopeParam(q url.Values) (kind, path string, ok bool) {
	if p := q.Get("project"); p != "" {
		return "project", p, config.ValidPath(p)
	}
	if g := q.Get("group"); g != "" {
		return "group", g, config.ValidPath(g)
	}
	return "", "", false
}

func (s *Server) handleScope(w http.ResponseWriter, r *http.Request) {
	kind, p, ok := scopeParam(r.URL.Query())
	if !ok {
		writeErr(w, http.StatusBadRequest, "pass a valid group or project path")
		return
	}
	item, err := s.svc.Scope(r.Context(), kind, p)
	if err != nil {
		s.upstreamErr(w, r, err)
		return
	}
	writeJSON(w, item)
}

func (s *Server) handleGroups(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	kind, p, ok := scopeParam(q)
	if !ok {
		writeErr(w, http.StatusBadRequest, "pass a valid group or project path")
		return
	}
	by := q.Get("by")
	if !dashboard.GroupByKinds[by] || (by == "project" && kind != "group") {
		writeErr(w, http.StatusBadRequest, "unknown grouping")
		return
	}
	win, err := dashboard.ParseWindow(q.Get("window"))
	if err != nil {
		writeErr(w, http.StatusBadRequest, err.Error())
		return
	}
	f := dashboard.Filter{Ref: strings.TrimSpace(q.Get("ref")), Status: q.Get("status")}
	if len(f.Ref) > 255 {
		writeErr(w, http.StatusBadRequest, "ref is too long")
		return
	}
	open := map[string]bool{}
	for _, k := range q["open"] {
		if len(open) >= 20 || len(k) > 600 {
			writeErr(w, http.StatusBadRequest, "too many open groups")
			return
		}
		open[k] = true
	}
	page, err := s.svc.GroupedPage(r.Context(), kind, p, by, win, f, open)
	if err != nil {
		s.upstreamErr(w, r, err)
		return
	}
	writeJSON(w, page)
}

func (s *Server) handleRuns(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	project, name := q.Get("project"), q.Get("name")
	page, err := strconv.Atoi(q.Get("page"))
	if !config.ValidPath(project) || name == "" || len(name) > 255 || err != nil || page < 1 || page > 50 {
		writeErr(w, http.StatusBadRequest, "pass a valid project, name and page")
		return
	}
	out, err := s.svc.OlderRuns(r.Context(), project, name, page)
	if err != nil {
		s.upstreamErr(w, r, err)
		return
	}
	writeJSON(w, out)
}

func (s *Server) handleLogo(w http.ResponseWriter, r *http.Request) {
	if s.logo == nil {
		http.NotFound(w, r)
		return
	}
	w.Header().Set("Content-Type", http.DetectContentType(s.logo))
	if strings.HasSuffix(strings.ToLower(s.cfg.LogoFile), ".svg") {
		w.Header().Set("Content-Type", "image/svg+xml")
		// SVG can carry script; never let it run in our origin.
		w.Header().Set("Content-Security-Policy", "default-src 'none'; style-src 'unsafe-inline'; sandbox")
	}
	w.Header().Set("Cache-Control", "public, max-age=3600")
	w.Write(s.logo)
}

func (s *Server) handleUI(w http.ResponseWriter, r *http.Request) {
	name := strings.TrimPrefix(path.Clean(r.URL.Path), "/")
	if name != "" {
		if st, err := fs.Stat(s.ui, name); err == nil && !st.IsDir() {
			if strings.HasPrefix(name, "assets/") {
				w.Header().Set("Cache-Control", "public, max-age=31536000, immutable")
			}
			http.ServeFileFS(w, r, s.ui, name)
			return
		}
		if strings.HasPrefix(name, "assets/") {
			http.NotFound(w, r)
			return
		}
	}
	index, err := fs.ReadFile(s.ui, "index.html")
	if err != nil {
		http.Error(w, "UI not built: run `make build`", http.StatusServiceUnavailable)
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set("Cache-Control", "no-cache")
	w.Write(index)
}

func (s *Server) upstreamErr(w http.ResponseWriter, r *http.Request, err error) {
	var ge *gitlab.Error
	if !errors.As(err, &ge) {
		if msg := err.Error(); strings.HasPrefix(msg, "unknown status") || strings.HasPrefix(msg, "unknown grouping") {
			writeErr(w, http.StatusBadRequest, err.Error())
			return
		}
		s.log.Error("request failed", "path", r.URL.Path, "err", err)
		writeErr(w, http.StatusInternalServerError, "internal error")
		return
	}
	s.log.Warn("gitlab error", "path", r.URL.Path, "status", ge.Status, "err", ge.Message)
	switch ge.Status {
	case http.StatusNotFound:
		writeErr(w, http.StatusNotFound, ge.Message)
	case http.StatusTooManyRequests:
		if ge.RetryAfter != "" {
			w.Header().Set("Retry-After", ge.RetryAfter)
		}
		writeErr(w, http.StatusServiceUnavailable, ge.Message)
	default:
		writeErr(w, http.StatusBadGateway, ge.Message)
	}
}

func writeJSON(w http.ResponseWriter, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "no-store")
	json.NewEncoder(w).Encode(v)
}

func writeErr(w http.ResponseWriter, status int, msg string) {
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(status)
	json.NewEncoder(w).Encode(map[string]string{"error": msg})
}

func isDigits(s string) bool {
	if s == "" || len(s) > 20 {
		return false
	}
	for _, r := range s {
		if r < '0' || r > '9' {
			return false
		}
	}
	return true
}

func securityHeaders(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		h := w.Header()
		h.Set("Content-Security-Policy", "default-src 'self'; img-src 'self' data: https: http:; object-src 'none'; base-uri 'none'; form-action 'none'; frame-ancestors 'none'")
		h.Set("X-Content-Type-Options", "nosniff")
		h.Set("Referrer-Policy", "same-origin")
		h.Set("X-Frame-Options", "DENY")
		if r.TLS != nil {
			h.Set("Strict-Transport-Security", "max-age=31536000")
		}
		next.ServeHTTP(w, r)
	})
}

type statusWriter struct {
	http.ResponseWriter
	status int
}

func (w *statusWriter) WriteHeader(code int) {
	w.status = code
	w.ResponseWriter.WriteHeader(code)
}

func (s *Server) logRequests(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		start := time.Now()
		sw := &statusWriter{ResponseWriter: w, status: http.StatusOK}
		next.ServeHTTP(sw, r)
		if strings.HasPrefix(r.URL.Path, "/api/") || sw.status >= 400 {
			s.log.Info("http", "method", r.Method, "path", r.URL.Path, "status", sw.status, "dur", time.Since(start).Round(time.Millisecond))
		}
	})
}
