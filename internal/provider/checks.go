package provider

import (
	"context"
	"net/url"
	"sort"
	"time"
)

const CheckLimit = 200

type Check struct {
	ID           string    `json:"id"`
	Name         string    `json:"name"`
	Kind         string    `json:"kind"`
	State        string    `json:"state"`
	Status       string    `json:"status"`
	Conclusion   string    `json:"conclusion,omitempty"`
	HTMLURL      string    `json:"htmlURL,omitempty"`
	Stage        string    `json:"stage,omitempty"`
	Description  string    `json:"description,omitempty"`
	StartedAt    time.Time `json:"startedAt"`
	FinishedAt   time.Time `json:"finishedAt"`
	AllowFailure bool      `json:"allowFailure,omitempty"`
}

type Checks struct {
	HeadSHA    string    `json:"headSHA"`
	Summary    string    `json:"summary"`
	Items      []Check   `json:"items"`
	Incomplete bool      `json:"incomplete"`
	Notices    []string  `json:"notices"`
	FetchedAt  time.Time `json:"fetchedAt"`
	HTMLURL    string    `json:"htmlURL"`
}

type ChecksProvider interface {
	Checks(context.Context, DiffSource) (Checks, error)
}

// SafeCheckURL permits external CI destinations but never executable schemes
// or URLs containing credentials. These links are never fetched by the server.
func SafeCheckURL(value string) string {
	parsed, err := url.Parse(value)
	if err != nil || parsed.Host == "" || parsed.User != nil || (parsed.Scheme != "https" && parsed.Scheme != "http") {
		return ""
	}
	return value
}

func BoundChecks(checks Checks) Checks {
	if checks.Items == nil {
		checks.Items = []Check{}
	}
	if checks.Notices == nil {
		checks.Notices = []string{}
	}
	if len(checks.Items) > CheckLimit {
		checks.Items = checks.Items[:CheckLimit]
		checks.Incomplete = true
		checks.Notices = append(checks.Notices, "Only the first 200 results are shown.")
	}
	for i := range checks.Items {
		checks.Items[i].HTMLURL = SafeCheckURL(checks.Items[i].HTMLURL)
	}
	sort.SliceStable(checks.Items, func(i, j int) bool {
		a, b := checks.Items[i], checks.Items[j]
		if a.Kind == "pipeline" && b.Kind != "pipeline" {
			return true
		}
		if b.Kind == "pipeline" && a.Kind != "pipeline" {
			return false
		}
		if a.Stage != b.Stage {
			return a.Stage < b.Stage
		}
		if a.Name != b.Name {
			return a.Name < b.Name
		}
		return a.ID < b.ID
	})
	checks.FetchedAt = time.Now().UTC()
	return checks
}
