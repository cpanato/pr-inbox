package provider

import (
	"context"
	"errors"
)

// Mergeability of a change request as reported by the host.
const (
	MergeClean       = "clean"       // can be merged now
	MergeConflicting = "conflicting" // has merge conflicts
	MergeBlocked     = "blocked"     // required reviews or checks are missing
	MergeBehind      = "behind"      // base branch moved on; needs a rebase
	MergeUnknown     = "unknown"     // the host has not computed it yet
)

// Review decisions shared by every provider.
const (
	ReviewApproved         = "approved"
	ReviewChangesRequested = "changes_requested"
	ReviewRequired         = "review_required"
	ReviewNone             = "none"
)

// Merge methods. Providers report which ones they support.
const (
	MergeMethodMerge  = "merge"
	MergeMethodSquash = "squash"
	MergeMethodRebase = "rebase"
)

var (
	ErrBotsUnsupported = errors.New("this provider does not support bot pull request management")
	ErrBotInvalid      = errors.New("invalid bot pull request action")
)

// BotQuery selects open change requests opened by an automation account (for
// example dependabot) inside one organization or group.
type BotQuery struct {
	Org    string
	Author string
	// Repos optionally narrows the query to a few repositories ("owner/name"),
	// which must belong to Org. It keeps paging and refreshing cheap in huge orgs.
	Repos []string
}

// MaxBotRepos is the most repositories a single query may cover.
const MaxBotRepos = 10

// BotRepo is a repository with open automation PRs.
type BotRepo struct {
	Repo  string `json:"repo"`
	Count int    `json:"count"`
}

type BotRepoResult struct {
	Repos      []BotRepo
	Incomplete bool
}

// BotPullRequest is a change request with the extra state needed to decide
// whether it can be approved and merged.
type BotPullRequest struct {
	PullRequest
	HeadSHA     string `json:"headSHA"`
	Mergeable   string `json:"mergeable"`
	ReviewState string `json:"review"`
	Branch      string `json:"branch"`
	BaseBranch  string `json:"baseBranch"`
}

type BotResult struct {
	Items      []BotPullRequest
	TotalCount int
	Incomplete bool
}

// BotProvider is implemented by providers that can list automation PRs and act
// on them. Actions are pinned to the head commit that was listed, so a PR that
// changed in the meantime is never approved or merged unseen.
type BotProvider interface {
	// BotOrgs lists the organizations (groups) the viewer belongs to, so the
	// UI can offer them as choices instead of requiring command-line flags.
	BotOrgs(ctx context.Context) ([]string, error)
	// BotRepos is a cheap query listing which repositories have open
	// automation PRs, without CI or review details.
	BotRepos(ctx context.Context, query BotQuery) (BotRepoResult, error)
	BotPullRequests(ctx context.Context, query BotQuery, limit int) (BotResult, error)
	BotApprove(ctx context.Context, pr BotPullRequest) error
	BotMerge(ctx context.Context, pr BotPullRequest, method string) error
	BotComment(ctx context.Context, pr BotPullRequest, body string) error
	// BotClose closes the pull request without merging it.
	BotClose(ctx context.Context, pr BotPullRequest) error
	MergeMethods() []string
}
