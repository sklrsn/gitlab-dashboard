package config

import (
	"errors"
	"fmt"
	"net/url"
	"os"
	"strconv"
	"strings"
	"time"
)

type Scope struct {
	Kind string `json:"kind"` // "group" or "project"
	Path string `json:"path"`
}

type Config struct {
	GitLabURL        *url.URL
	GitLabToken      string
	GitLabCAFile     string
	ListenAddr       string
	TLSCertFile      string
	TLSKeyFile       string
	AppTitle         string
	LogoFile         string
	DefaultScope     *Scope
	GroupFanoutLimit int
	CacheTTL         time.Duration
	RequestTimeout   time.Duration
}

func Load(getenv func(string) string) (*Config, error) {
	var errs []error
	c := &Config{
		GitLabCAFile: getenv("GITLAB_CA_FILE"),
		ListenAddr:   orDefault(getenv("LISTEN_ADDR"), ":8080"),
		TLSCertFile:  getenv("TLS_CERT_FILE"),
		TLSKeyFile:   getenv("TLS_KEY_FILE"),
		AppTitle:     orDefault(getenv("APP_TITLE"), "Pipelines"),
		LogoFile:     getenv("LOGO_FILE"),
	}

	raw := strings.TrimRight(getenv("GITLAB_URL"), "/")
	if raw == "" {
		errs = append(errs, errors.New("GITLAB_URL is required"))
	} else if u, err := url.Parse(raw); err != nil || (u.Scheme != "https" && u.Scheme != "http") || u.Host == "" {
		errs = append(errs, fmt.Errorf("GITLAB_URL %q must be an http(s) URL", raw))
	} else {
		c.GitLabURL = u
	}

	token, err := secret(getenv, "GITLAB_TOKEN")
	if err != nil {
		errs = append(errs, err)
	} else if token == "" {
		errs = append(errs, errors.New("GITLAB_TOKEN or GITLAB_TOKEN_FILE is required"))
	}
	c.GitLabToken = token

	if (c.TLSCertFile == "") != (c.TLSKeyFile == "") {
		errs = append(errs, errors.New("TLS_CERT_FILE and TLS_KEY_FILE must be set together"))
	}

	if s := getenv("DEFAULT_SCOPE"); s != "" {
		scope, err := ParseScope(s)
		if err != nil {
			errs = append(errs, fmt.Errorf("DEFAULT_SCOPE: %w", err))
		}
		c.DefaultScope = scope
	}

	c.GroupFanoutLimit = intEnv(getenv, "GROUP_FANOUT_LIMIT", 50, 1, 200, &errs)
	c.CacheTTL = durEnv(getenv, "CACHE_TTL", 10*time.Second, &errs)
	c.RequestTimeout = durEnv(getenv, "GITLAB_TIMEOUT", 20*time.Second, &errs)

	return c, errors.Join(errs...)
}

// ParseScope accepts "group:path/to/group" or "project:path/to/project".
func ParseScope(s string) (*Scope, error) {
	kind, path, ok := strings.Cut(s, ":")
	if !ok || (kind != "group" && kind != "project") || !ValidPath(path) {
		return nil, fmt.Errorf("%q must look like group:<path> or project:<path>", s)
	}
	return &Scope{Kind: kind, Path: path}, nil
}

// ValidPath checks a GitLab namespace/project full path.
func ValidPath(p string) bool {
	if p == "" || len(p) > 1024 || strings.HasPrefix(p, "/") || strings.HasSuffix(p, "/") || strings.Contains(p, "//") {
		return false
	}
	for _, r := range p {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9':
		case r == '-', r == '_', r == '.', r == '/':
		default:
			return false
		}
	}
	for _, seg := range strings.Split(p, "/") {
		if seg == "." || seg == ".." {
			return false
		}
	}
	return true
}

func secret(getenv func(string) string, name string) (string, error) {
	if f := getenv(name + "_FILE"); f != "" {
		b, err := os.ReadFile(f)
		if err != nil {
			return "", fmt.Errorf("%s_FILE: %w", name, err)
		}
		return strings.TrimSpace(string(b)), nil
	}
	return strings.TrimSpace(getenv(name)), nil
}

func orDefault(v, d string) string {
	if v == "" {
		return d
	}
	return v
}

func intEnv(getenv func(string) string, name string, def, min, max int, errs *[]error) int {
	v := getenv(name)
	if v == "" {
		return def
	}
	n, err := strconv.Atoi(v)
	if err != nil || n < min || n > max {
		*errs = append(*errs, fmt.Errorf("%s must be an integer between %d and %d", name, min, max))
		return def
	}
	return n
}

func durEnv(getenv func(string) string, name string, def time.Duration, errs *[]error) time.Duration {
	v := getenv(name)
	if v == "" {
		return def
	}
	d, err := time.ParseDuration(v)
	if err != nil || d < 0 {
		*errs = append(*errs, fmt.Errorf("%s must be a duration like 10s", name))
		return def
	}
	return d
}
