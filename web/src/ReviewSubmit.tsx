import React, { useEffect, useRef, useState } from "react";
import { request, type APIError } from "./api";

type Preview = { token: string; account: string; repo: string; number: number; headSHA: string; body: string; action: string; expiresAt: string };
type Operation = { operationID: string; status: string; headSHA: string; draftVersion: string; headStatus?: string; draftCleared: boolean; result: { id: number; htmlURL: string; state: string } };
type Props = { providerId: string; itemId: number; revision: string; version: string; dirty: boolean; disabled: boolean; demo: boolean; providerURL: string; csrfToken: string; onBusyChange: (busy: boolean) => void; onSubmitted: () => void };

export function ReviewSubmit({ providerId, itemId, revision, version, dirty, disabled, demo, providerURL, csrfToken, onBusyChange, onSubmitted }: Props) {
  const recoverSubmission = useRef(false);
  const [operation, setOperation] = useState<Operation | null>(null);
  const [preview, setPreview] = useState<Preview | null>(null);
  const [error, setError] = useState<APIError | null>(null);
  const [loading, setLoading] = useState(true);
  const [busy, setBusy] = useState(false);
  const [uncertain, setUncertain] = useState(false);
  const [attempt, setAttempt] = useState(0);
  const [acknowledged, setAcknowledged] = useState(false);
  const suffix = `?provider=${encodeURIComponent(providerId)}&id=${itemId}&revision=${encodeURIComponent(revision)}`;
  const blocked = uncertain || operation?.status === "sending" || operation?.status === "unknown";
  useEffect(() => {
    const controller = new AbortController(); setLoading(true);
    void request<Operation | null>(`/api/review${suffix}`, { signal: controller.signal }).then((data) => { if (!controller.signal.aborted) { setOperation(data); setUncertain(false); if (data?.status === "confirmed" && recoverSubmission.current) { recoverSubmission.current = false; setError(null); onSubmitted(); } } })
      .catch((reason: unknown) => { if (!controller.signal.aborted) { setError(reason instanceof Error ? reason : new Error("Could not load the last review operation.")); setUncertain(true); } })
      .finally(() => { if (!controller.signal.aborted) setLoading(false); });
    return () => controller.abort();
  }, [suffix, attempt]);
  useEffect(() => { onBusyChange(busy); }, [busy, onBusyChange]);
  useEffect(() => () => onBusyChange(false), [onBusyChange]);
  useEffect(() => { setPreview(null); setAcknowledged(false); }, [dirty, version]);

  async function prepare() {
    setBusy(true); setError(null); setPreview(null); setAcknowledged(false);
    try { setPreview(await request<Preview>(`/api/review/prepare${suffix}`, { method: "POST", body: JSON.stringify({ version }) }, csrfToken)); }
    catch (reason) { setError(reason instanceof Error ? reason : new Error("Could not prepare the review.")); if ((reason as APIError).code === "submission_unknown") setUncertain(true); }
    finally { setBusy(false); }
  }
  async function submit() {
    if (!preview || !acknowledged) return;
    setBusy(true); setError(null);
    const token = preview.token; setPreview(null);
    try {
      const result = await request<Operation>(`/api/review/submit${suffix}`, { method: "POST", body: JSON.stringify({ token }) }, csrfToken);
      setOperation(result); setUncertain(false); onSubmitted();
    } catch (reason) {
      setError(reason instanceof Error ? reason : new Error("Could not confirm the review outcome."));
      // Even a lost local response may follow a successful remote write.
      recoverSubmission.current = true; setUncertain(true); setAttempt((value) => value + 1);
    } finally { setBusy(false); }
  }
  async function reconcile() {
    setBusy(true); setError(null);
    try {
      const result = await request<Operation>(`/api/review/reconcile${suffix}`, { method: "POST", body: "{}" }, csrfToken);
      setOperation(result); setUncertain(false);
      if (result.status === "confirmed") onSubmitted();
    } catch (reason) { setError(reason instanceof Error ? reason : new Error("The outcome could not be confirmed. Check GitHub before doing anything else.")); }
    finally { setBusy(false); }
  }
  return <section className="review-submit" aria-label="Submit GitHub review">
    <h4>{demo ? "Simulate GitHub review" : "Submit GitHub review"}</h4>
    <p className="detail-format-note">{demo ? "Demo only: no GitHub requests or notifications are sent." : "Submitting sends the saved draft to GitHub and may notify participants. A review applies to the inspected commit; it does not imply merge readiness."}</p>
    {loading && <p role="status">Checking the last review operation…</p>}
    {error && <div className="notice error-notice" role="alert"><p>{error.message}</p><a href={providerURL} target="_blank" rel="noreferrer">Check this PR on GitHub ↗</a></div>}
    {blocked && <div className="notice" role="status"><p>A review may be in progress or have an unknown outcome. No new submission is allowed until its outcome is confirmed. Your saved draft is preserved.</p><button className="refresh-button" type="button" disabled={busy || loading || disabled} onClick={() => void reconcile()}>Check outcome on GitHub</button><button className="refresh-button" type="button" disabled={busy || loading} onClick={() => { setError(null); setAttempt((value) => value + 1); }}>Reload operation status</button></div>}
    {operation?.status === "confirmed" && <div className="notice review-success" role="status"><p>{demo ? "Simulation confirmed" : "Review confirmed"}: {operation.result.state} for commit <code>{operation.headSHA}</code>. <a href={operation.result.htmlURL} target="_blank" rel="noreferrer">View review on GitHub ↗</a></p>{operation.headStatus === "changed" && <p>The PR head changed during submission. This review applies to the displayed commit; reload details to inspect the newer changes.</p>}{operation.headStatus === "unknown" && <p>The current PR head could not be checked. Reload details before reviewing further.</p>}{!operation.draftCleared && <p>Draft cleanup could not be confirmed or a newer draft was preserved. This review is already confirmed; do not submit it again.</p>}</div>}
    {operation?.status === "confirmed" && (!operation.draftCleared || operation.headStatus === "unknown") && <button className="refresh-button" type="button" disabled={busy || loading || disabled} onClick={() => void reconcile()}>Refresh confirmed review</button>}
    {operation?.status === "rejected" && <p className="notice" role="status">GitHub rejected the last attempt. The draft is preserved; check your permissions and preview it again before retrying.</p>}
    <button className="refresh-button" type="button" disabled={disabled || loading || busy || dirty || !version || !!blocked || operation?.status === "confirmed" && operation.draftVersion === version} onClick={() => void prepare()}>Preview review submission</button>
    {dirty && <p className="detail-format-note">Save your latest edits before preparing a submission.</p>}
    {preview && <div className="review-confirmation notice" role="group" aria-labelledby="review-confirmation-heading">
      <h4 id="review-confirmation-heading">{demo ? "Confirm simulated review" : "Confirm GitHub review"}</h4>
      <dl><dt>Account</dt><dd>@{preview.account}</dd><dt>Pull request</dt><dd>{preview.repo} #{preview.number}</dd><dt>Commit</dt><dd><code>{preview.headSHA}</code></dd><dt>Action</dt><dd>{preview.action.replace(/_/g, " ")}</dd></dl>
      <pre className="review-confirmation-body" tabIndex={0}>{preview.body || "(No review text)"}</pre>
      <p className="detail-format-note">Confirmation expires at {new Date(preview.expiresAt).toLocaleTimeString()}.</p>
      <label><input type="checkbox" checked={acknowledged} onChange={(event) => setAcknowledged(event.target.checked)} /> I checked the account, PR, commit, action and full text.</label>
      <div className="draft-actions"><button className="refresh-button" type="button" disabled={!acknowledged || busy || disabled || dirty} onClick={() => void submit()}>{demo ? "Confirm simulation" : "Confirm and submit review"}</button><button className="refresh-button" type="button" disabled={busy} onClick={() => setPreview(null)}>Cancel submission</button></div>
    </div>}
  </section>;
}
