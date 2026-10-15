import React, { useEffect, useState } from "react";
import { request, type APIError } from "./api";

type Check = {
  id: string;
  name: string;
  kind: string;
  state: string;
  status: string;
  conclusion?: string;
  htmlURL?: string;
  stage?: string;
  description?: string;
  startedAt: string;
  finishedAt: string;
  allowFailure?: boolean;
};
type Checks = {
  headSHA: string;
  summary: string;
  items: Check[];
  incomplete: boolean;
  notices: string[];
  fetchedAt: string;
};
type Props = { providerId: string; itemId: number; revision: string; headSHA: string; providerName: string; providerKind: string; providerURL: string };

const summaryLabels: Record<string, string> = { success: "CI passing", failure: "CI failing", pending: "CI pending", none: "No CI reported", unknown: "CI unknown" };
const safeURL = (value?: string) => { if (!value) return ""; try { const url = new URL(value); return ["https:", "http:"].includes(url.protocol) && !url.username && !url.password ? value : ""; } catch { return ""; } };
const displayResult = (value: string) => value.replace(/_/g, " ");

export function ChecksPanel({ providerId, itemId, revision, headSHA, providerName, providerKind, providerURL }: Props) {
  const [checks, setChecks] = useState<Checks | null>(null);
  const [error, setError] = useState<APIError | null>(null);
  const [attempt, setAttempt] = useState(0);
  const [loading, setLoading] = useState(true);

  useEffect(() => {
    const controller = new AbortController();
    setLoading(true);
    setError(null);
    void request<Checks>(`/api/checks?provider=${encodeURIComponent(providerId)}&id=${itemId}&revision=${encodeURIComponent(revision)}`, { signal: controller.signal })
      .then((data) => {
        if (controller.signal.aborted) return;
        if (data.headSHA !== headSHA) throw new Error("Check results do not match the inspected commit. Reload details.");
        setChecks(data);
      })
      .catch((reason: unknown) => { if (!controller.signal.aborted) setError(reason instanceof Error ? reason : new Error("Could not load checks.")); })
      .finally(() => { if (!controller.signal.aborted) setLoading(false); });
    return () => controller.abort();
  }, [providerId, itemId, revision, headSHA, attempt]);

  return (
    <section className="checks-section" aria-labelledby="checks-heading" aria-busy={loading}>
      <div className="checks-heading"><h3 id="checks-heading">Checks{checks ? ` (${checks.items.length})` : ""}</h3><button className="refresh-button" type="button" disabled={loading} onClick={() => setAttempt((value) => value + 1)}>Refresh checks</button></div>
      <p className="detail-format-note">Results for commit <code>{headSHA.slice(0, 12)}</code>. {providerKind === "gitlab" ? "GitLab keeps the selected pipeline; reload details to select a newer one." : "Results may update as checks finish."}</p>
      {error && <div className="notice error-notice checks-error" role="alert"><p>{error.message}</p>{checks && <p>Showing previously loaded results; they may be stale.</p>}<a href={providerURL} target="_blank" rel="noreferrer">View checks on {providerName} ↗</a></div>}
      {loading && <p className="detail-loading" role="status">Loading checks…</p>}
      {checks && (
        <>
          <p className="checks-summary" role="status"><strong>{error ? "Previous summary: " : "Provider summary: "}{summaryLabels[checks.summary] ?? checks.summary}</strong> · Loaded at {new Date(checks.fetchedAt).toLocaleTimeString()}</p>
          {checks.incomplete && <div className="notice checks-warning" role="status"><strong>Some check results may be missing.</strong>{checks.notices.map((notice, index) => <p key={index}>{notice}</p>)}<p><a href={providerURL} target="_blank" rel="noreferrer">View all results on {providerName} ↗</a></p></div>}
          {checks.items.length === 0 ? <p>{checks.summary === "none" && !checks.incomplete ? "No checks were reported for this inspected revision. Reload details to discover a newer pipeline." : "No check entries could be shown. Open the provider for full results."}</p> : (
            <ul className="checks-list">
              {checks.items.map((check) => (
                <li key={check.id} className="check-entry">
                  <div className="check-name"><strong>{check.name}</strong><span className="check-kind">{check.stage ? `${check.stage} · ` : ""}{check.kind}</span></div>
                  <span className={`check-result ci-${["success", "failure", "pending", "none", "unknown"].includes(check.state) ? check.state : "unknown"}`}>{displayResult(check.conclusion || check.status || check.state)}</span>
                  {safeURL(check.htmlURL) && <a href={safeURL(check.htmlURL)} target="_blank" rel="noreferrer">Details ↗</a>}
                  {(check.description || check.allowFailure) && <p className="check-description">{check.description}{check.allowFailure ? " · Allowed to fail" : ""}</p>}
                </li>
              ))}
            </ul>
          )}
        </>
      )}
    </section>
  );
}
