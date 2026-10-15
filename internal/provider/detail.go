package provider

import "context"

// Detail is a fresh, read-only view of a discovered change request.
// CI describes HeadSHA; unknown means that revision's status was unavailable.
type Detail struct {
	PullRequest
	Description  string `json:"description"`
	SourceBranch string `json:"sourceBranch"`
	TargetBranch string `json:"targetBranch"`
	HeadSHA      string `json:"headSHA"`
	State        string `json:"state"`
}

// DetailProvider is optional so discovery-only providers remain usable.
type DetailProvider interface {
	Detail(context.Context, PullRequest) (Detail, error)
}
