package provider

import (
	"fmt"
	"strings"
	"testing"
)

func TestPatchCounts(t *testing.T) {
	for _, tc := range []struct {
		name, patch    string
		added, removed int
		valid          bool
	}{
		{"replacement", "@@ -1 +1 @@\n-old\n+new\n", 1, 1, true},
		{"addition", "@@ -0,0 +1,2 @@\n+first\n+second\n", 2, 0, true},
		{"deletion", "@@ -1 +0,0 @@\n-old\n", 0, 1, true},
		{"context and multiple hunks", "@@ -1,2 +1,2 @@ function\n same\n-old\n+new\n@@ -20 +20 @@\n-a\n+b", 2, 2, true},
		{"no final newline marker", "@@ -1 +1 @@\n-old\n\\ No newline at end of file\n+new\n\\ No newline at end of file\n", 1, 1, true},
		{"cut off hunk", "@@ -1,2 +1,2 @@\n same\n-old\n", 0, 0, false},
		{"missing body", "@@ -1 +1 @@\n", 0, 0, false},
		{"extra lines", "@@ -1 +1 @@\n-old\n+new\n+extra\n", 0, 0, false},
		{"bad header", "@@ nonsense @@\n+new", 0, 0, false},
		{"overflow", "@@ -1,9999999999999999999999999 +1 @@\n-old\n+new", 0, 0, false},
		{"binary", "Binary files a/x and b/x differ", 0, 0, false},
		{"empty", "", 0, 0, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			added, removed, valid := PatchCounts(tc.patch)
			if valid != tc.valid || valid && (added != tc.added || removed != tc.removed) {
				t.Fatalf("got %d,%d,%v", added, removed, valid)
			}
		})
	}
}

func TestBoundDiffDisclosesOmissions(t *testing.T) {
	good := "@@ -1 +1 @@\n-old\n+new\n"
	diff := BoundDiff(Diff{TotalFiles: 6, Files: []DiffFile{
		{Path: "good", Patch: good},
		{Path: "binary", Patch: "Binary files a/x and b/x differ"},
		{Path: "missing"},
		{Path: "truncated", Patch: "@@ -1 +1 @@\n-old"},
		{Path: "large", Patch: strings.Repeat("x", DiffFileByteLimit+1)},
		{Path: "provider omitted", Patch: good, Omission: "Collapsed by provider"},
	}})
	if !diff.Incomplete || diff.Files[0].Patch != good || diff.Files[0].Omission != "" {
		t.Fatalf("diff=%+v", diff)
	}
	for _, file := range diff.Files[1:] {
		if file.Patch != "" || file.Omission == "" {
			t.Fatalf("omission=%+v", file)
		}
	}
}

func TestBoundDiffBudgetsAndUnknownCoverage(t *testing.T) {
	patch := fmt.Sprintf("@@ -1,4000 +1,4000 @@\n%s", strings.Repeat(" "+strings.Repeat("x", 60)+"\n", 4000))
	files := make([]DiffFile, 30)
	for i := range files {
		files[i] = DiffFile{Path: fmt.Sprint(i), Patch: patch}
	}
	diff := BoundDiff(Diff{TotalFiles: len(files), Files: files})
	bytes := 0
	omitted := 0
	for _, file := range diff.Files {
		bytes += len(file.Patch)
		if file.Omission != "" {
			omitted++
		}
	}
	if !diff.Incomplete || bytes > DiffByteLimit || omitted == 0 {
		t.Fatalf("bytes=%d omitted=%d incomplete=%v", bytes, omitted, diff.Incomplete)
	}
	files = make([]DiffFile, DiffFileLimit+1)
	for i := range files {
		files[i] = DiffFile{Path: fmt.Sprint(i), Patch: "@@ -0,0 +1 @@\n+new\n"}
	}
	diff = BoundDiff(Diff{TotalFiles: len(files), Files: files})
	if len(diff.Files) != DiffFileLimit || !diff.Incomplete || len(diff.Notices) == 0 {
		t.Fatal("file cap was not disclosed")
	}
	diff = BoundDiff(Diff{TotalFiles: -1})
	if !diff.Incomplete || len(diff.Notices) == 0 {
		t.Fatal("unknown coverage was not disclosed")
	}
	diff = BoundDiff(Diff{TotalFiles: 0})
	if diff.Incomplete || diff.Files == nil {
		t.Fatal("empty diff must be explicit and complete")
	}
}
