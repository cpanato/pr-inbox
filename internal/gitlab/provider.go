package gitlab

import (
	"context"
	"fmt"
	"net/url"
	"os"
	"os/exec"
	"regexp"
	"strings"
	"time"

	"github.com/cpanato/pr-inbox/internal/provider"
)

var hostPattern = regexp.MustCompile(`^[a-z0-9](?:[a-z0-9.-]*[a-z0-9])?(?::[0-9]{1,5})?$`)

// Provider adapts Client to the provider-neutral interface.
type Provider struct {
	client *Client
	info   provider.Info
}

func NewProvider(client *Client, viewer provider.Viewer) *Provider {
	name := "GitLab"
	if client.host != DefaultHost {
		name = client.host
	}
	return &Provider{
		client: client,
		info: provider.Info{
			ID:     "gitlab:" + client.host,
			Name:   name,
			Kind:   "gitlab",
			Host:   client.host,
			Viewer: viewer,
		},
	}
}

func (p *Provider) Info() provider.Info { return p.info }

func (p *Provider) Search(ctx context.Context, query provider.Query, limit int) (provider.SearchResult, error) {
	return p.client.Search(ctx, p.info.Viewer, query, limit)
}

func (p *Provider) CIStatuses(ctx context.Context, prs []provider.PullRequest) (map[int64]string, error) {
	return p.client.CIStatuses(ctx, prs)
}

// NormalizeHost accepts "gitlab.example.com" or "https://gitlab.example.com/"
// and returns a lowercase host[:port]. Only HTTPS is supported, so a token is
// never sent in clear text.
func NormalizeHost(value string) (string, error) {
	value = strings.TrimSpace(value)
	if strings.HasPrefix(value, "http://") {
		return "", fmt.Errorf("GitLab host %q must use https", value)
	}
	value = strings.TrimPrefix(value, "https://")
	value = strings.TrimSuffix(value, "/")
	if parsed, err := url.Parse("https://" + value); err != nil || parsed.Path != "" || parsed.User != nil {
		return "", fmt.Errorf("invalid GitLab host %q; use a host name such as gitlab.example.com", value)
	}
	host := strings.ToLower(value)
	if !hostPattern.MatchString(host) {
		return "", fmt.Errorf("invalid GitLab host %q", value)
	}
	return host, nil
}

// TokenEnv returns the environment variable that holds the token for host:
// GITLAB_TOKEN for gitlab.com and GITLAB_TOKEN_<HOST> for any other instance
// (non-alphanumerics become "_"). A gitlab.com token is never reused for
// another host.
func TokenEnv(host string) string {
	if host == DefaultHost {
		return "GITLAB_TOKEN"
	}
	var b strings.Builder
	for _, r := range strings.ToUpper(host) {
		if (r >= 'A' && r <= 'Z') || (r >= '0' && r <= '9') {
			b.WriteRune(r)
		} else {
			b.WriteByte('_')
		}
	}
	return "GITLAB_TOKEN_" + b.String()
}

// Token reads the token from the environment, then from the GitLab CLI.
func Token(parent context.Context, host string) (string, error) {
	name := TokenEnv(host)
	if token := strings.TrimSpace(os.Getenv(name)); token != "" {
		return token, nil
	}
	ctx, cancel := context.WithTimeout(parent, 10*time.Second)
	defer cancel()
	output, err := exec.CommandContext(ctx, "glab", "config", "get", "token", "--host", host).Output()
	if err == nil {
		if token := strings.TrimSpace(string(output)); token != "" {
			return token, nil
		}
	}
	return "", fmt.Errorf("no GitLab token for %s; set %s to a personal access token with the read_api scope, or run `glab auth login --hostname %s`", host, name, host)
}
