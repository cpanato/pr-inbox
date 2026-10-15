package gitlab

import (
	"context"
	"fmt"
	"net/url"
	"strconv"

	"github.com/cpanato/pr-inbox/internal/provider"
)

type diffVersion struct {
	ID             int64  `json:"id"`
	MergeRequestID int64  `json:"merge_request_id"`
	HeadSHA        string `json:"head_commit_sha"`
	BaseSHA        string `json:"base_commit_sha"`
	StartSHA       string `json:"start_commit_sha"`
	State          string `json:"state"`
	RealSize       string `json:"real_size"`
}

func (p *Provider) Diff(ctx context.Context, source provider.DiffSource) (provider.Diff, error) {
	return p.client.Diff(ctx, source)
}

func (c *Client) Diff(ctx context.Context, source provider.DiffSource) (provider.Diff, error) {
	c.mu.Lock()
	projectID, ok := c.projects[source.ID]
	c.mu.Unlock()
	if !ok || projectID <= 0 || source.BaseSHA == "" || source.HeadSHA == "" || source.StartSHA == "" {
		return provider.Diff{}, provider.ErrRevisionUnavailable
	}
	path := fmt.Sprintf("/projects/%d/merge_requests/%d/versions", projectID, source.Number)
	var versions []diffVersion
	// Bound history discovery; if the inspected version has fallen outside this
	// window, fail visibly rather than returning the latest diff.
	if _, err := c.get(ctx, path, url.Values{"per_page": {"100"}}, &versions); err != nil {
		return provider.Diff{}, err
	}
	matches := func(version diffVersion) bool {
		return version.MergeRequestID == source.ID && version.HeadSHA == source.HeadSHA && version.BaseSHA == source.BaseSHA && version.StartSHA == source.StartSHA
	}
	var versionID int64
	for _, version := range versions {
		if matches(version) && version.ID > 0 {
			versionID = version.ID
			break
		}
	}
	if versionID == 0 {
		return provider.Diff{}, provider.ErrRevisionUnavailable
	}
	var response struct {
		diffVersion
		Diffs []struct {
			OldPath     string `json:"old_path"`
			NewPath     string `json:"new_path"`
			Diff        string `json:"diff"`
			NewFile     bool   `json:"new_file"`
			DeletedFile bool   `json:"deleted_file"`
			RenamedFile bool   `json:"renamed_file"`
			TooLarge    bool   `json:"too_large"`
			Collapsed   bool   `json:"collapsed"`
		} `json:"diffs"`
	}
	if _, err := c.get(ctx, path+"/"+strconv.FormatInt(versionID, 10), nil, &response); err != nil {
		return provider.Diff{}, err
	}
	if response.ID != versionID || !matches(response.diffVersion) || response.Diffs == nil {
		return provider.Diff{}, provider.ErrRevisionUnavailable
	}
	total, err := strconv.Atoi(response.RealSize)
	if err != nil || total < 0 {
		total = -1
	}
	diff := provider.Diff{HeadSHA: source.HeadSHA, BaseSHA: source.BaseSHA, TotalFiles: total, HTMLURL: source.HTMLURL + "/diffs?diff_id=" + strconv.FormatInt(versionID, 10)}
	if response.State != "collected" {
		diff.Incomplete = true
		diff.Notices = append(diff.Notices, "GitLab reports this diff version as "+response.State+"; some changes may be missing.")
	}
	for _, entry := range response.Diffs {
		status := "modified"
		switch {
		case entry.NewFile:
			status = "added"
		case entry.DeletedFile:
			status = "removed"
		case entry.RenamedFile:
			status = "renamed"
		}
		file := provider.DiffFile{Path: entry.NewPath, Status: status, Patch: entry.Diff}
		if entry.OldPath != entry.NewPath {
			file.OldPath = entry.OldPath
		}
		switch {
		case entry.TooLarge:
			file.Omission = "GitLab omitted this patch because the file is too large."
		case entry.Collapsed:
			file.Omission = "GitLab collapsed this patch; open it on the provider."
		}
		diff.Files = append(diff.Files, file)
	}
	return diff, nil
}
