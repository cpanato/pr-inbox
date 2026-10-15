package provider

import (
	"fmt"
	"testing"
)

func TestBoundChecksLimitsResultsAndSanitizesLinks(t *testing.T) {
	items := make([]Check, CheckLimit+1)
	for i := range items {
		items[i] = Check{ID: fmt.Sprint(i), Name: "Test", HTMLURL: "javascript:alert(1)"}
	}
	checks := BoundChecks(Checks{Items: items})
	if !checks.Incomplete || len(checks.Items) != CheckLimit || len(checks.Notices) == 0 || checks.FetchedAt.IsZero() {
		t.Fatal("missing limit disclosure")
	}
	if checks.Items[0].HTMLURL != "" {
		t.Fatal("unsafe URL retained")
	}
	for _, value := range []string{"data:text/html,x", "//evil.example", "https://user:secret@ci.example/x", "relative/path"} {
		if SafeCheckURL(value) != "" {
			t.Errorf("unsafe link %s", value)
		}
	}
	if SafeCheckURL("https://ci.example/job/1") != "https://ci.example/job/1" {
		t.Fatal("external CI links should work")
	}
	empty := BoundChecks(Checks{})
	if empty.Items == nil || empty.Notices == nil {
		t.Fatal("empty results must be explicit")
	}
}
