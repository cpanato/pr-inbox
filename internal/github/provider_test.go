package github

import (
	"strings"
	"testing"

	"github.com/cpanato/pr-inbox/internal/provider"
)

func TestSearchQuery(t *testing.T) {
	got, err := searchQuery(provider.Query{Role: provider.ReviewRequested, Org: "acme"}, "me")
	if err != nil {
		t.Fatal(err)
	}
	if want := baseQuery + " org:acme review-requested:me"; got != want {
		t.Fatalf("query = %q, want %q", got, want)
	}
	got, err = searchQuery(provider.Query{Role: provider.Authored}, "me")
	if err != nil || got != baseQuery+" author:me" {
		t.Fatalf("query = %q, err = %v", got, err)
	}
	if !strings.Contains(got, "archived:false") {
		t.Fatal("queries must exclude archived repositories")
	}
}

func TestSearchQueryRejectsInvalidInput(t *testing.T) {
	if _, err := searchQuery(provider.Query{Role: provider.Authored, Org: "evil org"}, "me"); err == nil || !strings.Contains(err.Error(), "invalid organization") {
		t.Fatalf("err = %v, want invalid organization", err)
	}
	if _, err := searchQuery(provider.Query{Role: "other"}, "me"); err == nil {
		t.Fatal("unknown role must be rejected")
	}
}
