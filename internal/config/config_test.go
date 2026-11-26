package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func env(m map[string]string) func(string) string { return func(k string) string { return m[k] } }

func TestLoadDefaults(t *testing.T) {
	c, err := Load(env(map[string]string{"GITLAB_URL": "https://gitlab.example.com/", "GITLAB_TOKEN": " tok "}))
	if err != nil {
		t.Fatal(err)
	}
	if c.GitLabURL.String() != "https://gitlab.example.com" || c.GitLabToken != "tok" || c.ListenAddr != ":8080" ||
		c.GroupFanoutLimit != 50 || c.CacheTTL != 10*time.Second || c.DefaultScope != nil {
		t.Errorf("unexpected defaults: %+v", c)
	}
}

func TestLoadErrorsAreCollected(t *testing.T) {
	_, err := Load(env(map[string]string{"GITLAB_URL": "ftp://x", "TLS_CERT_FILE": "a", "GROUP_FANOUT_LIMIT": "0", "DEFAULT_SCOPE": "team:x"}))
	if err == nil {
		t.Fatal("expected errors")
	}
	for _, want := range []string{"GITLAB_URL", "GITLAB_TOKEN", "TLS_CERT_FILE and TLS_KEY_FILE", "GROUP_FANOUT_LIMIT", "DEFAULT_SCOPE"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("missing %q in %v", want, err)
		}
	}
}

func TestTokenFile(t *testing.T) {
	f := filepath.Join(t.TempDir(), "token")
	os.WriteFile(f, []byte("from-file\n"), 0o600)
	c, err := Load(env(map[string]string{"GITLAB_URL": "https://g", "GITLAB_TOKEN_FILE": f, "GITLAB_TOKEN": "ignored"}))
	if err != nil || c.GitLabToken != "from-file" {
		t.Fatalf("token=%q err=%v", c.GitLabToken, err)
	}
}

func TestParseScope(t *testing.T) {
	s, err := ParseScope("project:platform/infra/terraform")
	if err != nil || s.Kind != "project" || s.Path != "platform/infra/terraform" {
		t.Fatalf("%+v %v", s, err)
	}
	for _, bad := range []string{"platform", "user:x", "group:", "group:/x", "group:a/../b", "group:a b", "group:a?b"} {
		if _, err := ParseScope(bad); err == nil {
			t.Errorf("%q should be rejected", bad)
		}
	}
}
