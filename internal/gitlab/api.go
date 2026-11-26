package gitlab

import (
	"context"
	"net/url"
	"strconv"
	"strings"
	"time"
)

const pipelineFields = `
fragment PipelineFields on Pipeline {
  id iid name status source ref sha path createdAt startedAt finishedAt duration
  detailedStatus { group label }
  user { username name avatarUrl }
  commit { title }
  project { fullPath name webUrl }
  upstream { id path project { fullPath } }
  downstream { count }
  stages { nodes { name detailedStatus { group label } } }
}`

const projectPipelinesQuery = `
query ProjectPipelines($fullPath: ID!, $first: Int!, $after: String, $ref: String, $status: PipelineStatusEnum) {
  project(fullPath: $fullPath) {
    pipelines(first: $first, after: $after, ref: $ref, status: $status) {
      pageInfo { hasNextPage endCursor }
      nodes { ...PipelineFields }
    }
  }
}` + pipelineFields

const downstreamQuery = `
query PipelineDownstream($fullPath: ID!, $iid: ID!) {
  project(fullPath: $fullPath) {
    pipeline(iid: $iid) {
      downstream(first: 100) { nodes { ...PipelineFields } }
    }
  }
}` + pipelineFields

type Status struct {
	Group string `json:"group"`
	Label string `json:"label"`
}

type Pipeline struct {
	ID             string     `json:"id"`
	IID            string     `json:"iid"`
	Name           string     `json:"name"`
	Status         string     `json:"status"`
	Source         string     `json:"source"`
	Ref            string     `json:"ref"`
	SHA            string     `json:"sha"`
	Path           string     `json:"path"`
	CreatedAt      *time.Time `json:"createdAt"`
	StartedAt      *time.Time `json:"startedAt"`
	FinishedAt     *time.Time `json:"finishedAt"`
	Duration       *int       `json:"duration"`
	DetailedStatus Status     `json:"detailedStatus"`
	User           *struct {
		Username  string `json:"username"`
		Name      string `json:"name"`
		AvatarURL string `json:"avatarUrl"`
	} `json:"user"`
	Commit *struct {
		Title string `json:"title"`
	} `json:"commit"`
	Project struct {
		FullPath string `json:"fullPath"`
		Name     string `json:"name"`
		WebURL   string `json:"webUrl"`
	} `json:"project"`
	Upstream *struct {
		ID      string `json:"id"`
		Path    string `json:"path"`
		Project struct {
			FullPath string `json:"fullPath"`
		} `json:"project"`
	} `json:"upstream"`
	Downstream struct {
		Count int `json:"count"`
	} `json:"downstream"`
	Stages struct {
		Nodes []struct {
			Name           string `json:"name"`
			DetailedStatus Status `json:"detailedStatus"`
		} `json:"nodes"`
	} `json:"stages"`
}

// NumericID extracts 123 from "gid://gitlab/Ci::Pipeline/123".
func NumericID(gid string) int64 {
	n, _ := strconv.ParseInt(gid[strings.LastIndexByte(gid, '/')+1:], 10, 64)
	return n
}

type PipelineFilter struct {
	Ref    string
	Status string // GraphQL PipelineStatusEnum value, e.g. "FAILED"
}

type PipelinePage struct {
	Pipelines   []Pipeline
	HasNextPage bool
	EndCursor   string
}

func (c *Client) ProjectPipelines(ctx context.Context, fullPath string, first int, after string, f PipelineFilter) (PipelinePage, error) {
	vars := map[string]any{"fullPath": fullPath, "first": first}
	if after != "" {
		vars["after"] = after
	}
	if f.Ref != "" {
		vars["ref"] = f.Ref
	}
	if f.Status != "" {
		vars["status"] = f.Status
	}
	var data struct {
		Project *struct {
			Pipelines struct {
				PageInfo struct {
					HasNextPage bool   `json:"hasNextPage"`
					EndCursor   string `json:"endCursor"`
				} `json:"pageInfo"`
				Nodes []Pipeline `json:"nodes"`
			} `json:"pipelines"`
		} `json:"project"`
	}
	if err := c.GraphQL(ctx, "ProjectPipelines", projectPipelinesQuery, vars, &data); err != nil {
		return PipelinePage{}, err
	}
	if data.Project == nil {
		return PipelinePage{}, &Error{Status: 404, Message: "project " + fullPath + " not found, or the token can't see it"}
	}
	p := data.Project.Pipelines
	return PipelinePage{Pipelines: p.Nodes, HasNextPage: p.PageInfo.HasNextPage, EndCursor: p.PageInfo.EndCursor}, nil
}

func (c *Client) Downstream(ctx context.Context, fullPath, iid string) ([]Pipeline, error) {
	var data struct {
		Project *struct {
			Pipeline *struct {
				Downstream struct {
					Nodes []Pipeline `json:"nodes"`
				} `json:"downstream"`
			} `json:"pipeline"`
		} `json:"project"`
	}
	if err := c.GraphQL(ctx, "PipelineDownstream", downstreamQuery, map[string]any{"fullPath": fullPath, "iid": iid}, &data); err != nil {
		return nil, err
	}
	if data.Project == nil || data.Project.Pipeline == nil {
		return nil, &Error{Status: 404, Message: "pipeline not found, or the token can't see it"}
	}
	return data.Project.Pipeline.Downstream.Nodes, nil
}

// Bridge is a trigger job that starts a child or multi-project pipeline.
type Bridge struct {
	ID                 int64  `json:"id"`
	Name               string `json:"name"`
	Stage              string `json:"stage"`
	Status             string `json:"status"`
	WebURL             string `json:"web_url"`
	DownstreamPipeline *struct {
		ID        int64      `json:"id"`
		ProjectID int64      `json:"project_id"`
		Ref       string     `json:"ref"`
		SHA       string     `json:"sha"`
		Status    string     `json:"status"`
		WebURL    string     `json:"web_url"`
		CreatedAt *time.Time `json:"created_at"`
	} `json:"downstream_pipeline"`
}

func (c *Client) Bridges(ctx context.Context, fullPath string, pipelineID int64) ([]Bridge, error) {
	var out []Bridge
	q := url.Values{"per_page": {"100"}}
	err := c.REST(ctx, "projects/"+ProjectID(fullPath)+"/pipelines/"+strconv.FormatInt(pipelineID, 10)+"/bridges", q, &out)
	return out, err
}

type Project struct {
	ID                int64  `json:"id"`
	PathWithNamespace string `json:"path_with_namespace"`
	NameWithNamespace string `json:"name_with_namespace"`
	WebURL            string `json:"web_url"`
}

// GroupProjects lists non-archived projects in a group and its subgroups, most recently active first.
func (c *Client) GroupProjects(ctx context.Context, groupPath string, limit int) ([]Project, error) {
	var out []Project
	q := url.Values{
		"include_subgroups": {"true"},
		"archived":          {"false"},
		"order_by":          {"last_activity_at"},
		"sort":              {"desc"},
		"simple":            {"true"},
		"per_page":          {strconv.Itoa(limit)},
	}
	err := c.REST(ctx, "groups/"+ProjectID(groupPath)+"/projects", q, &out)
	return out, err
}

type Group struct {
	ID       int64  `json:"id"`
	FullPath string `json:"full_path"`
	FullName string `json:"full_name"`
	WebURL   string `json:"web_url"`
}

func (c *Client) SearchGroups(ctx context.Context, search string, limit int) ([]Group, error) {
	var out []Group
	q := url.Values{"all_available": {"true"}, "order_by": {"path"}, "per_page": {strconv.Itoa(limit)}}
	if search != "" {
		q.Set("search", search)
	}
	err := c.REST(ctx, "groups", q, &out)
	return out, err
}

func (c *Client) SearchProjects(ctx context.Context, search string, limit int) ([]Project, error) {
	var out []Project
	q := url.Values{
		"simple":   {"true"},
		"archived": {"false"},
		"order_by": {"last_activity_at"},
		"per_page": {strconv.Itoa(limit)},
	}
	if search == "" {
		q.Set("membership", "true")
	} else {
		q.Set("search", search)
		q.Set("search_namespaces", "true")
	}
	err := c.REST(ctx, "projects", q, &out)
	return out, err
}

// GetGroup returns one group's display name and URL.
func (c *Client) GetGroup(ctx context.Context, fullPath string) (Group, error) {
	var out Group
	err := c.REST(ctx, "groups/"+ProjectID(fullPath), url.Values{"with_projects": {"false"}}, &out)
	return out, err
}

// GetProject returns one project's display name and URL.
func (c *Client) GetProject(ctx context.Context, fullPath string) (Project, error) {
	var out Project
	err := c.REST(ctx, "projects/"+ProjectID(fullPath), nil, &out)
	return out, err
}

// RESTPipeline is the short pipeline record from the REST list endpoint.
type RESTPipeline struct {
	ID        int64      `json:"id"`
	IID       int64      `json:"iid"`
	Name      string     `json:"name"`
	Status    string     `json:"status"`
	Source    string     `json:"source"`
	Ref       string     `json:"ref"`
	SHA       string     `json:"sha"`
	WebURL    string     `json:"web_url"`
	CreatedAt *time.Time `json:"created_at"`
}

// PipelinesByName lists a project's pipelines with this exact name, newest first.
func (c *Client) PipelinesByName(ctx context.Context, fullPath, name string, page, perPage int) ([]RESTPipeline, error) {
	var out []RESTPipeline
	q := url.Values{
		"name":     {name},
		"order_by": {"id"},
		"sort":     {"desc"},
		"page":     {strconv.Itoa(page)},
		"per_page": {strconv.Itoa(perPage)},
	}
	err := c.REST(ctx, "projects/"+ProjectID(fullPath)+"/pipelines", q, &out)
	return out, err
}

type MergeRequest struct {
	IID          int64  `json:"iid"`
	Title        string `json:"title"`
	SourceBranch string `json:"source_branch"`
	TargetBranch string `json:"target_branch"`
	WebURL       string `json:"web_url"`
}

// MergeRequestsByIID fetches up to 100 of a project's merge requests by IID.
func (c *Client) MergeRequestsByIID(ctx context.Context, fullPath string, iids []int64) ([]MergeRequest, error) {
	var out []MergeRequest
	q := url.Values{"per_page": {"100"}}
	for _, iid := range iids {
		q.Add("iids[]", strconv.FormatInt(iid, 10))
	}
	err := c.REST(ctx, "projects/"+ProjectID(fullPath)+"/merge_requests", q, &out)
	return out, err
}
