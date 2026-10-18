package tui

import (
	"errors"
	"net/url"
	"strconv"
	"strings"
	"unicode"

	"github.com/cpanato/pr-inbox/internal/provider"
)

// Provider data and saved state are untrusted terminal text, never ANSI output.
func safe(text string) string {
	return strings.Map(func(r rune) rune {
		if unicode.IsControl(r) || unicode.In(r, unicode.Cf) {
			return ' '
		}
		return r
	}, text)
}
func targetURL(info provider.Info, pr provider.PullRequest) (string, error) {
	invalid := errors.New("The selected item has no valid HTTPS provider link.")
	parsed, err := url.Parse(pr.HTMLURL)
	if err != nil || parsed.Scheme != "https" || parsed.Hostname() == "" || info.Host == "" || parsed.User != nil || !strings.EqualFold(parsed.Host, info.Host) || parsed.RawQuery != "" || parsed.Fragment != "" || pr.Number <= 0 || pr.Repo == "" {
		return "", invalid
	}
	segments := strings.Split(pr.Repo, "/")
	if len(segments) < 2 || strings.ContainsAny(pr.Repo, "\\?#") {
		return "", invalid
	}
	for _, segment := range segments {
		if segment == "" || segment == "." || segment == ".." || safe(segment) != segment {
			return "", invalid
		}
	}
	if info.Kind == "github" && len(segments) != 2 {
		return "", invalid
	}
	suffix := "/pull/"
	if info.Kind == "gitlab" {
		suffix = "/-/merge_requests/"
	} else if info.Kind != "github" {
		return "", invalid
	}
	if parsed.Path != "/"+pr.Repo+suffix+strconv.Itoa(pr.Number) {
		return "", invalid
	}
	return parsed.String(), nil
}
