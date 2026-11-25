package dashboard

import (
	"net/url"
	"strconv"
	"strings"
	"time"

	"gitlab-dashboard/internal/gitlab"
)

type ProjectRef struct {
	Path   string `json:"path"`
	Name   string `json:"name"`
	WebURL string `json:"webUrl"`
}

type User struct {
	Username  string `json:"username"`
	Name      string `json:"name"`
	AvatarURL string `json:"avatarUrl,omitempty"`
}

type Stage struct {
	Name   string `json:"name"`
	Status string `json:"status"`
	Label  string `json:"label"`
}

type Upstream struct {
	ID          int64  `json:"id"`
	ProjectPath string `json:"projectPath"`
	WebURL      string `json:"webUrl"`
}

type Pipeline struct {
	Key          string     `json:"key"`
	ID           int64      `json:"id,omitempty"`
	IID          string     `json:"iid,omitempty"`
	Name         string     `json:"name,omitempty"`
	Title        string     `json:"title"`
	Status       string     `json:"status"`
	StatusLabel  string     `json:"statusLabel"`
	Source       string     `json:"source,omitempty"`
	Ref          string     `json:"ref,omitempty"`
	SHA          string     `json:"sha,omitempty"`
	WebURL       string     `json:"webUrl"`
	Project      ProjectRef `json:"project"`
	CreatedAt    *time.Time `json:"createdAt,omitempty"`
	StartedAt    *time.Time `json:"startedAt,omitempty"`
	FinishedAt   *time.Time `json:"finishedAt,omitempty"`
	Duration     *int       `json:"duration,omitempty"`
	User         *User      `json:"user,omitempty"`
	CommitTitle  string     `json:"commitTitle,omitempty"`
	Stages       []Stage    `json:"stages"`
	ChildCount   int        `json:"childCount"`
	Upstream     *Upstream  `json:"upstream,omitempty"`
	TriggerJob   string     `json:"triggerJob,omitempty"`
	CrossProject bool       `json:"crossProject,omitempty"`
	// Limited marks a child the token can't read in detail; only bridge data is known.
	Limited bool `json:"limited,omitempty"`
}

func convert(base *url.URL, p gitlab.Pipeline) Pipeline {
	id := gitlab.NumericID(p.ID)
	out := Pipeline{
		Key:         "p" + strconv.FormatInt(id, 10),
		ID:          id,
		IID:         p.IID,
		Name:        p.Name,
		Status:      normStatus(p.DetailedStatus.Group, p.Status),
		StatusLabel: p.DetailedStatus.Label,
		Source:      p.Source,
		Ref:         p.Ref,
		SHA:         p.SHA,
		WebURL:      absURL(base, p.Path),
		Project:     ProjectRef{Path: p.Project.FullPath, Name: p.Project.Name, WebURL: p.Project.WebURL},
		CreatedAt:   p.CreatedAt,
		StartedAt:   p.StartedAt,
		FinishedAt:  p.FinishedAt,
		Duration:    p.Duration,
		ChildCount:  p.Downstream.Count,
		Stages:      make([]Stage, 0, len(p.Stages.Nodes)),
	}
	if out.StatusLabel == "" {
		out.StatusLabel = strings.ToLower(p.Status)
	}
	if p.User != nil {
		out.User = &User{Username: p.User.Username, Name: p.User.Name, AvatarURL: absURL(base, p.User.AvatarURL)}
	}
	if p.Commit != nil {
		out.CommitTitle = p.Commit.Title
	}
	if p.Upstream != nil {
		out.Upstream = &Upstream{
			ID:          gitlab.NumericID(p.Upstream.ID),
			ProjectPath: p.Upstream.Project.FullPath,
			WebURL:      absURL(base, p.Upstream.Path),
		}
	}
	for _, s := range p.Stages.Nodes {
		out.Stages = append(out.Stages, Stage{Name: s.Name, Status: normStatus(s.DetailedStatus.Group, ""), Label: s.DetailedStatus.Label})
	}
	out.Title = title(out)
	return out
}

func title(p Pipeline) string {
	switch {
	case p.Name != "":
		return p.Name
	case p.TriggerJob != "":
		return p.TriggerJob
	case p.CommitTitle != "":
		return p.CommitTitle
	default:
		return "Pipeline #" + strconv.FormatInt(p.ID, 10)
	}
}

// normStatus prefers GitLab's detailed status group (e.g. "success-with-warnings").
func normStatus(group, status string) string {
	if group != "" {
		return strings.ToLower(group)
	}
	if status != "" {
		return strings.ToLower(status)
	}
	return "unknown"
}

func absURL(base *url.URL, ref string) string {
	if ref == "" {
		return ""
	}
	u, err := url.Parse(ref)
	if err != nil {
		return ""
	}
	// GitLab's relative paths already include any relative URL root.
	r := base.ResolveReference(u)
	if r.Scheme != "http" && r.Scheme != "https" {
		return ""
	}
	return r.String()
}
