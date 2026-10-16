import React, { useEffect, useState } from "react";
import { request, type APIError } from "./api";

type DiffFile = {
  path: string;
  oldPath?: string;
  status: string;
  patch: string;
  omission?: string;
};

type Diff = {
  headSHA: string;
  baseSHA: string;
  files: DiffFile[];
  totalFiles: number;
  incomplete: boolean;
  notices: string[];
  htmlURL: string;
};

type Progress = { paths: string[]; previousRevision: boolean };

type Props = {
 csrfToken: string;
 readOnly: boolean;
  providerId: string;
  itemId: number;
  revision: string;
  headSHA: string;
  providerName: string;
  providerURL: string;
  onReload: () => void;
};

export function DiffWorkspace({ csrfToken, readOnly, providerId, itemId, revision, headSHA, providerName, providerURL, onReload }: Props) {
  const [diff, setDiff] = useState<Diff | null>(null);
  const [error, setError] = useState<APIError | null>(null);
  const [attempt, setAttempt] = useState(0);
  const [selectedIndex, setSelectedIndex] = useState(0);
  const [plainText, setPlainText] = useState(false);

  useEffect(() => {
    const controller = new AbortController();
    setDiff(null);
    setError(null);
    setSelectedIndex(0);
    void request<Diff>(`/api/diff?provider=${encodeURIComponent(providerId)}&id=${itemId}&revision=${encodeURIComponent(revision)}`, { signal: controller.signal })
      .then((data) => {
        if (controller.signal.aborted) return;
        if (data.headSHA !== headSHA) throw new Error("The diff does not match the displayed commit. Reload details before continuing.");
        setDiff(data);
      })
      .catch((reason: unknown) => {
        if (!controller.signal.aborted) setError(reason instanceof Error ? reason : new Error("Could not load the diff."));
      });
    return () => controller.abort();
  }, [providerId, itemId, revision, headSHA, attempt]);

  const [progress, setProgress] = useState<Progress | null>(null);
  const [progressError, setProgressError] = useState("");
  const [progressAttempt, setProgressAttempt] = useState(0);
  const [saving, setSaving] = useState(false);
  const markerURL = `/api/viewed?provider=${encodeURIComponent(providerId)}&id=${itemId}&revision=${encodeURIComponent(revision)}`;
  useEffect(() => {
    const controller = new AbortController();
    setProgress(null);
    setProgressError("");
    void request<Progress>(markerURL, { signal: controller.signal })
      .then((data) => { if (!controller.signal.aborted) setProgress(data); })
      .catch((reason: unknown) => { if (!controller.signal.aborted) setProgressError(reason instanceof Error ? reason.message : "Could not load review progress."); });
    return () => controller.abort();
  }, [markerURL, progressAttempt]);

  async function toggleViewed(path: string) {
    if (!progress || saving) return;
    const viewed = !progress.paths.includes(path);
    setSaving(true);
    setProgressError("");
    try {
      await request(markerURL, { method: "POST", body: JSON.stringify({ path, viewed }) }, csrfToken);
      setProgress((current) => current ? { ...current, paths: viewed ? [...current.paths, path] : current.paths.filter((entry) => entry !== path) } : current);
    } catch (reason) {
      setProgressError(reason instanceof Error ? reason.message : "Could not save review progress.");
    } finally { setSaving(false); }
  }
  const isViewed = (file: DiffFile) => !file.omission && !!file.patch && !!progress?.paths.includes(file.path);
  const viewedCount = diff?.files.filter(isViewed).length ?? 0;
  const selected = diff?.files[selectedIndex];
  return (
    <section className="diff-section" aria-labelledby="diff-heading">
      <div className="diff-heading">
        <h3 id="diff-heading">Changed files{diff ? ` (${diff.files.length}${diff.totalFiles >= 0 ? ` of ${diff.totalFiles}` : ""})` : ""}</h3>
        <button className="refresh-button" type="button" onClick={onReload}>Reload details</button>
      </div>
      <p className="detail-format-note">Pinned to <code>{headSHA.slice(0, 12)}</code>. Reload details to inspect a newer revision.</p>
      {progressError && <div className="notice error-notice" role="alert"><p>{progressError}</p><button type="button" className="refresh-button" onClick={() => setProgressAttempt((value) => value + 1)} disabled={saving}>Reload progress</button></div>}
      {progress?.previousRevision && <p className="notice" role="status">Saved progress exists for another revision. Files in this revision must be viewed separately.</p>}
      {diff && progress && <p className="detail-format-note" role="status">{viewedCount} of {diff.totalFiles >= 0 ? diff.totalFiles : diff.files.length} files viewed{diff.totalFiles < 0 ? " (total unknown)" : ""}. Saved locally for this revision.{diff.incomplete ? " Unavailable or unlisted patches remain pending." : ""}{readOnly ? " Read-only mode: progress cannot be changed." : ""}</p>}
      {error ? (
        <div className="notice error-notice" role="alert">
          <p>{error.message}</p>
          {error.code !== "revision_unavailable" && <button className="refresh-button" type="button" onClick={() => setAttempt((value) => value + 1)}>Retry diff</button>}
          <p><a href={providerURL} target="_blank" rel="noreferrer">Continue on {providerName} ↗</a></p>
        </div>
      ) : !diff ? (
        <p role="status" className="detail-loading">Loading diff for this revision…</p>
      ) : (
        <>
          <p className="detail-format-note">Compared with merge base <code>{diff.baseSHA.slice(0, 12)}</code>. <a href={diff.htmlURL} target="_blank" rel="noreferrer">Open provider diff ↗</a></p>
          {diff.incomplete && (
            <div className="notice diff-warning" role="status">
              <strong>Some changes are not shown in full.</strong>
              {diff.notices.map((notice, index) => <p key={index}>{notice}</p>)}
              <p>{diff.files.filter((file) => file.omission).length} listed file(s) have no complete text patch. Limits: 300 files, 5 MiB total, 256 KiB or 10,000 lines per file.</p>
              <a href={diff.htmlURL} target="_blank" rel="noreferrer">Continue on {providerName} ↗</a>
            </div>
          )}
          {diff.files.length === 0 ? (
            <p>{diff.incomplete ? "No file patches could be loaded." : "No changed files in this revision."}</p>
          ) : (
            <div className="diff-workspace">
              <nav className="diff-files" aria-label="Changed files">
                {diff.files.map((file, index) => (
                  <button key={`${index}:${file.path}`} className={index === selectedIndex ? "diff-file active" : "diff-file"} type="button" aria-current={index === selectedIndex ? "true" : undefined} onClick={() => setSelectedIndex(index)}>
                    <span>{file.path}</span>
                    <small>{file.status}{file.omission ? " · patch unavailable" : !progress ? " · progress unavailable" : isViewed(file) ? " · viewed" : " · pending"}</small>
                  </button>
                ))}
              </nav>
              {selected && (
                <section className="diff-patch" aria-label={`Changes in ${selected.path}`}>
                  <div className="diff-patch-heading">
                    <h4>{selected.oldPath ? `${selected.oldPath} → ` : ""}{selected.path}</h4>
                    <span className="diff-status">{selected.status}</span>
                    <label className="viewed-toggle"><input type="checkbox" checked={!!isViewed(selected)} disabled={readOnly || saving || !progress || !!selected.omission || !selected.patch} onChange={() => void toggleViewed(selected.path)} /> Viewed</label>
                  </div>
                  {selected.omission ? (
                    <div className="notice"><p>{selected.omission}</p><a href={diff.htmlURL} target="_blank" rel="noreferrer">View on {providerName} ↗</a></div>
                  ) : (
                    <>
                      <label className="diff-text-toggle"><input type="checkbox" checked={plainText} onChange={(event) => setPlainText(event.target.checked)} /> Plain text diff</label>
                      {plainText ? (
                        <pre className="unified-diff" tabIndex={0} aria-label="Plain text unified diff">{selected.patch}</pre>
                      ) : (
                        <pre className="unified-diff" tabIndex={0} aria-label="Unified diff; plus lines are additions, minus lines are deletions"><code>{selected.patch.split("\n").map((line, index) => <span key={index} className={line.startsWith("@@") ? "diff-hunk" : line.startsWith("+") ? "diff-add" : line.startsWith("-") ? "diff-remove" : "diff-context"}>{line}{"\n"}</span>)}</code></pre>
                      )}
                    </>
                  )}
                </section>
              )}
            </div>
          )}
        </>
      )}
    </section>
  );
}
