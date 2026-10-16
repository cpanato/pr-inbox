import React, { useEffect, useState } from "react";
import { request, type APIError } from "./api";

type Draft = { headSHA: string; body: string; action: string; version: string; updatedAt: string; deleted?: boolean };
type Drafts = { current: Draft; previous: { headSHA: string; updatedAt: string }[]; truncated: boolean };
type Props = { providerId: string; itemId: number; revision: string; headSHA: string; csrfToken: string; readOnly: boolean; onDirtyChange: (dirty: boolean) => void };
const bodyLimit = 64 * 1024;

export function ReviewDraft({ providerId, itemId, revision, headSHA, csrfToken, readOnly, onDirtyChange }: Props) {
  const [saved, setSaved] = useState<Drafts | null>(null);
  const [body, setBody] = useState("");
  const [action, setAction] = useState("comment");
  const [error, setError] = useState<APIError | null>(null);
  const [loading, setLoading] = useState(true);
  const [saving, setSaving] = useState(false);
  const [attempt, setAttempt] = useState(0);
  const [historical, setHistorical] = useState<Draft | null>(null);
  const [historyHead, setHistoryHead] = useState("");
  const [historyError, setHistoryError] = useState("");
  const url = `/api/draft?provider=${encodeURIComponent(providerId)}&id=${itemId}&revision=${encodeURIComponent(revision)}`;
  const dirty = saved !== null && (body !== saved.current.body || action !== saved.current.action);
  const bytes = new TextEncoder().encode(body).length;

  useEffect(() => {
    const controller = new AbortController();
    setLoading(true);
    setError(null);
    void request<Drafts>(url, { signal: controller.signal }).then((data) => {
      if (controller.signal.aborted) return;
      if (data.current.headSHA !== headSHA) throw new Error("The saved draft does not match this commit.");
      setSaved(data); setBody(data.current.body); setAction(data.current.action);
    }).catch((reason: unknown) => { if (!controller.signal.aborted) setError(reason instanceof Error ? reason : new Error("Could not load the draft.")); })
      .finally(() => { if (!controller.signal.aborted) setLoading(false); });
    return () => controller.abort();
  }, [url, headSHA, attempt]);

  useEffect(() => { onDirtyChange(dirty || saving); }, [dirty, saving, onDirtyChange]);
  useEffect(() => () => onDirtyChange(false), [onDirtyChange]);
  useEffect(() => {
    if (!dirty && !saving) return;
    const preventLoss = (event: BeforeUnloadEvent) => { event.preventDefault(); event.returnValue = ""; };
    window.addEventListener("beforeunload", preventLoss);
    return () => window.removeEventListener("beforeunload", preventLoss);
  }, [dirty, saving]);

  useEffect(() => {
    setHistorical(null); setHistoryError("");
    if (!historyHead) return;
    const controller = new AbortController();
    void request<Draft>(`${url}&head=${encodeURIComponent(historyHead)}`, { signal: controller.signal })
      .then((data) => { if (!controller.signal.aborted) setHistorical(data); })
      .catch((reason: unknown) => { if (!controller.signal.aborted) setHistoryError(reason instanceof Error ? reason.message : "Could not load the earlier draft."); });
    return () => controller.abort();
  }, [url, historyHead]);

  async function save(remove = false) {
    if (!saved || saving || readOnly || loading) return;
    if (remove && !window.confirm("Delete the saved draft for this commit and discard any unsaved changes?")) return;
    setSaving(true); setError(null);
    try {
      const current = await request<Draft>(url, { method: "POST", body: JSON.stringify({ body: remove ? "" : body, action: remove ? "comment" : action, version: saved.current.version, delete: remove }) }, csrfToken);
      setSaved({ ...saved, current }); setBody(current.body); setAction(current.action);
    } catch (reason) { setError(reason instanceof Error ? reason : new Error("Could not save the draft.")); }
    finally { setSaving(false); }
  }
  function reload() {
    if (dirty && !window.confirm("Replace your unsaved text with the saved draft? Copy any text you want to keep first.")) return;
    setAttempt((value) => value + 1);
  }

  return (
    <section className="review-draft" aria-labelledby="review-draft-heading">
      <h3 id="review-draft-heading">Local review draft</h3>
      <p className="detail-format-note">Saved only on this device for commit <code>{headSHA.slice(0, 12)}</code>. Nothing is submitted to the provider.</p>
      {loading && <p role="status">Loading saved draft…</p>}
      {error && <div className="notice error-notice" role="alert"><p>{error.message}</p><p>Your text is still in the editor. Copy it before reloading if you want to keep it.</p><button className="refresh-button" type="button" disabled={saving || loading} onClick={reload}>Reload saved draft</button></div>}
      <label className="draft-field">Review intent
        <select aria-label="Draft review intent" value={action} disabled={readOnly || !saved || loading || saving} onChange={(event) => setAction(event.target.value)}>
          <option value="comment">Comment</option><option value="approve">Approve</option><option value="request_changes">Request changes</option>
        </select>
      </label>
      <label className="draft-field">Review text
        <textarea aria-label="Review draft text" rows={8} value={body} readOnly={readOnly} disabled={!saved || loading || saving} onChange={(event) => setBody(event.target.value)} placeholder="Write a review to save locally…" />
      </label>
      <p className="detail-format-note">{bytes.toLocaleString()} / 65,536 bytes. Text is stored and displayed as plain text.</p>
      {bytes > bodyLimit && <p className="notice error-notice" role="alert">Draft exceeds the 64 KiB limit. Shorten the text before saving.</p>}
      <div className="draft-actions">
        <button className="refresh-button" type="button" disabled={readOnly || !saved || loading || saving || !dirty || bytes > bodyLimit} onClick={() => void save()}>Save draft</button>
        <button className="refresh-button" type="button" disabled={readOnly || !saved?.current.version || saved.current.deleted || loading || saving} onClick={() => void save(true)}>Delete draft</button>
        <span className="detail-format-note" role="status">{readOnly ? "Read-only mode: drafts cannot be changed." : saving ? "Saving…" : dirty ? "Unsaved changes — use Save draft before closing." : saved?.current.version && !saved.current.deleted ? `Saved locally at ${new Date(saved.current.updatedAt).toLocaleString()}.` : saved ? "No saved draft for this commit." : "Draft not loaded."}</span>
      </div>
      {saved && saved.previous.length > 0 && <div className="draft-history notice">
        <p>Drafts exist for other commits. They are preserved separately; inspect the current changes before reusing their text.</p>
        <label className="draft-field">Earlier draft
          <select aria-label="Earlier draft" value={historyHead} onChange={(event) => setHistoryHead(event.target.value)}>
            <option value="">Choose a commit…</option>
            {saved.previous.map((draft) => <option key={draft.headSHA} value={draft.headSHA}>{draft.headSHA.slice(0, 12)} · {new Date(draft.updatedAt).toLocaleString()}</option>)}
          </select>
        </label>
        {saved.truncated && <p>Only the 100 most recently saved earlier drafts are listed.</p>}
        {historyError && <p role="alert">{historyError}</p>}
        {historyHead && !historical && !historyError && <p role="status">Loading earlier draft…</p>}
        {historical && <>
          <p>Commit <code>{historical.headSHA}</code> · {historical.action.replace(/_/g, " ")}{historical.deleted ? " · deleted in another tab" : ""}</p>
          <textarea aria-label="Earlier draft text" rows={6} readOnly value={historical.body} />
          <button className="refresh-button" type="button" disabled={readOnly || loading || saving || historical.deleted} onClick={() => {
            if (dirty && !window.confirm("Replace your unsaved text with this earlier draft?")) return;
            setBody(historical.body); setAction(historical.action);
          }}>Copy into current draft</button>
        </>}
      </div>}
    </section>
  );
}
