// Package gitlab is a minimal read-only client for the GitLab GraphQL and REST APIs.
package gitlab

import (
	"bytes"
	"context"
	"crypto/tls"
	"crypto/x509"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"strings"
	"time"
)

type Client struct {
	base  *url.URL
	token string
	http  *http.Client
}

type Options struct {
	BaseURL *url.URL
	Token   string
	CAFile  string
	Timeout time.Duration
}

func New(o Options) (*Client, error) {
	tr := http.DefaultTransport.(*http.Transport).Clone()
	if o.CAFile != "" {
		pem, err := os.ReadFile(o.CAFile)
		if err != nil {
			return nil, fmt.Errorf("reading GITLAB_CA_FILE: %w", err)
		}
		pool, err := x509.SystemCertPool()
		if err != nil || pool == nil {
			pool = x509.NewCertPool()
		}
		if !pool.AppendCertsFromPEM(pem) {
			return nil, errors.New("GITLAB_CA_FILE contains no PEM certificates")
		}
		tr.TLSClientConfig = &tls.Config{RootCAs: pool, MinVersion: tls.VersionTLS12}
	}
	tr.MaxIdleConnsPerHost = 16
	return &Client{
		base:  o.BaseURL,
		token: o.Token,
		http:  &http.Client{Transport: tr, Timeout: o.Timeout},
	}, nil
}

// BaseURL is the GitLab instance URL, used to build links.
func (c *Client) BaseURL() *url.URL { return c.base }

// Error is a failed GitLab call, with a message safe to show in the UI.
type Error struct {
	Status     int
	Message    string
	RetryAfter string
}

func (e *Error) Error() string { return e.Message }

func (c *Client) do(req *http.Request, out any) error {
	req.Header.Set("Authorization", "Bearer "+c.token)
	req.Header.Set("Accept", "application/json")
	req.Header.Set("User-Agent", "gitlab-dashboard")
	resp, err := c.http.Do(req)
	if err != nil {
		return &Error{Status: http.StatusBadGateway, Message: "cannot reach GitLab: " + err.Error()}
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(io.LimitReader(resp.Body, 32<<20))
	if err != nil {
		return &Error{Status: http.StatusBadGateway, Message: "reading GitLab response: " + err.Error()}
	}
	switch {
	case resp.StatusCode == http.StatusUnauthorized:
		return &Error{Status: resp.StatusCode, Message: "GitLab rejected the token (expired, revoked, or wrong scope)"}
	case resp.StatusCode == http.StatusForbidden:
		return &Error{Status: resp.StatusCode, Message: "the token has no access to this resource"}
	case resp.StatusCode == http.StatusNotFound:
		return &Error{Status: resp.StatusCode, Message: "not found, or the token can't see it"}
	case resp.StatusCode == http.StatusTooManyRequests:
		return &Error{Status: resp.StatusCode, Message: "GitLab rate limit reached, try again shortly", RetryAfter: resp.Header.Get("Retry-After")}
	case resp.StatusCode >= 300:
		return &Error{Status: http.StatusBadGateway, Message: fmt.Sprintf("GitLab returned HTTP %d", resp.StatusCode)}
	}
	if err := json.Unmarshal(body, out); err != nil {
		return &Error{Status: http.StatusBadGateway, Message: "unexpected GitLab response: " + err.Error()}
	}
	return nil
}

// REST performs GET /api/v4/<path>.
func (c *Client) REST(ctx context.Context, path string, q url.Values, out any) error {
	u := c.base.JoinPath("/api/v4")
	u.RawPath = u.Path + "/" + path
	p, err := url.PathUnescape(u.RawPath)
	if err != nil {
		return err
	}
	u.Path = p
	u.RawQuery = q.Encode()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u.String(), nil)
	if err != nil {
		return err
	}
	return c.do(req, out)
}

type gqlError struct {
	Message string `json:"message"`
}

// GraphQL runs a named query against /api/graphql and decodes "data" into out.
func (c *Client) GraphQL(ctx context.Context, op, query string, vars map[string]any, out any) error {
	payload, err := json.Marshal(map[string]any{"operationName": op, "query": query, "variables": vars})
	if err != nil {
		return err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.base.JoinPath("/api/graphql").String(), bytes.NewReader(payload))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	var resp struct {
		Data   json.RawMessage `json:"data"`
		Errors []gqlError      `json:"errors"`
	}
	if err := c.do(req, &resp); err != nil {
		return err
	}
	if len(resp.Errors) > 0 {
		msgs := make([]string, 0, len(resp.Errors))
		for _, e := range resp.Errors {
			msgs = append(msgs, e.Message)
		}
		return &Error{Status: http.StatusBadGateway, Message: "GitLab GraphQL error: " + strings.Join(msgs, "; ")}
	}
	if err := json.Unmarshal(resp.Data, out); err != nil {
		return &Error{Status: http.StatusBadGateway, Message: "unexpected GitLab GraphQL response: " + err.Error()}
	}
	return nil
}

// ProjectID encodes a full path for use as a REST :id segment.
func ProjectID(fullPath string) string { return url.PathEscape(fullPath) }
