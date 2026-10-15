package demo

import (
	"context"
	"github.com/cpanato/pr-inbox/internal/provider"
)

func (c *Client) Diff(_ context.Context, source provider.DiffSource) (provider.Diff, error) {
	return provider.Diff{
		HeadSHA: source.HeadSHA, BaseSHA: "0123456789012345678901234567890123456789", TotalFiles: 5, HTMLURL: source.HTMLURL,
		Files: []provider.DiffFile{
			{Path: "internal/config/config.go", Status: "modified", Patch: "@@ -1,3 +1,4 @@\n package config\n-const retries = 1\n+const retries = 3\n+const timeout = 30\n // Defaults for API requests.\n"},
			{Path: "docs/retry-policy.md", OldPath: "docs/retries.md", Status: "renamed", Patch: "@@ -1 +1 @@\n-# Retries\n+# Retry policy\n"},
			{Path: "internal/config/config_test.go", Status: "added", Patch: "@@ -0,0 +1,2 @@\n+package config\n+// Verify retry defaults.\n"},
			{Path: "docs/legacy.txt", Status: "removed", Patch: "@@ -1 +0,0 @@\n-Obsolete configuration.\n"},
			{Path: "assets/logo.png", Status: "modified", Omission: "Binary file; no text diff is available."},
		},
	}, nil
}
