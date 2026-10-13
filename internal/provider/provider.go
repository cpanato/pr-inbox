// Package provider defines the code-host-neutral model and the interface that
// each supported host (GitHub, and later GitLab) implements.
package provider

import (
	"context"
	"time"
)

// CI states shared by every provider.
const (
	CISuccess = "success"
	CIFailure = "failure"
	CIPending = "pending"
	CINone    = "none"
	CIUnknown = "unknown"
)

// Role is why a change request shows up in the viewer's inbox.
type Role string

const (
	ReviewRequested Role = "review-requested"
	Authored        Role = "authored"
)

type Viewer struct {
	ID    int64  `json:"id"`
	Login string `json:"login"`
}

// PullRequest is a pull request (GitHub) or merge request (GitLab).
type PullRequest struct {
	ID        int64     `json:"id"`
	Number    int       `json:"number"`
	Title     string    `json:"title"`
	HTMLURL   string    `json:"html_url"`
	Repo      string    `json:"repo"`
	Author    string    `json:"author"`
	CreatedAt time.Time `json:"created_at"`
	UpdatedAt time.Time `json:"updated_at"`
	Draft     *bool     `json:"draft"`
	CI        string    `json:"ci"`
}

type SearchResult struct {
	Items      []PullRequest
	TotalCount int
}

// Query selects the open change requests for the viewer in a given role,
// optionally limited to one organization (group on GitLab).
type Query struct {
	Role Role
	Org  string
}

// Info identifies a provider instance, for example github.com or a
// self-hosted GitLab.
type Info struct {
	ID     string `json:"id"`
	Name   string `json:"name"`
	Kind   string `json:"kind"`
	Host   string `json:"host"`
	Viewer Viewer `json:"-"`
}

type Provider interface {
	Info() Info
	Search(ctx context.Context, query Query, limit int) (SearchResult, error)
	// CIStatuses returns the CI state keyed by pull request ID. Missing keys
	// are treated as CIUnknown.
	CIStatuses(ctx context.Context, prs []PullRequest) (map[int64]string, error)
}
