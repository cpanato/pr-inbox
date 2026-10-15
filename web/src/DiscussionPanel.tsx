import React, { useEffect, useState } from "react";
import { request } from "./api";

type Entry = {
  id: string;
  kind: string;
  author: string;
  body: string;
  createdAt: string;
  updatedAt: string;
  htmlURL: string;
  state?: string;
  threadID?: string;
  replyTo?: string;
  resolved?: boolean;
  path?: string;
  line?: number;
  side?: string;
  commitSHA?: string;
  outdated?: boolean;
  bodyTruncated: boolean;
};

type Discussion = {
  entries: Entry[];
  page: number;
  hasMore: boolean;
  truncated: boolean;
  notices: string[];
  fetchedAt: string;
  htmlURL: string;
};

type Props = {
  providerId: string;
  itemId: number;
  providerName: string;
  providerKind: string;
  providerURL: string;
  headSHA: string;
};

const kindLabels: Record<string, string> = { comment: "Comment", review: "Review", inline: "File discussion", note: "Note", system: "Activity" };
const stateLabels: Record<string, string> = { APPROVED: "Approved", CHANGES_REQUESTED: "Changes requested", COMMENTED: "Commented", DISMISSED: "Dismissed" };

export function DiscussionPanel({ providerId, itemId, providerName, providerKind, providerURL, headSHA }: Props) {
  const [entries, setEntries] = useState<Entry[]>([]);
  const [page, setPage] = useState(1);
  const [loadedPage, setLoadedPage] = useState(0);
  const [hasMore, setHasMore] = useState(false);
  const [truncated, setTruncated] = useState(false);
  const [notices, setNotices] = useState<string[]>([]);
  const [loading, setLoading] = useState(true);
  const [error, setError] = useState("");
  const [attempt, setAttempt] = useState(0);
  const [fetchedAt, setFetchedAt] = useState("");
  const [generation, setGeneration] = useState(0);

  useEffect(() => {
    const controller = new AbortController();
    setLoading(true);
    setError("");
    void request<Discussion>(`/api/discussion?provider=${encodeURIComponent(providerId)}&id=${itemId}&page=${page}`, { signal: controller.signal })
      .then((data) => {
        if (controller.signal.aborted) return;
        setEntries((current) => {
          const byId = new Map((page === 1 ? [] : current).map((entry) => [entry.id, entry]));
          for (const entry of data.entries) byId.set(entry.id, entry);
          return [...byId.values()].sort((a, b) => Date.parse(a.createdAt) - Date.parse(b.createdAt) || a.id.localeCompare(b.id));
        });
        setLoadedPage(data.page);
        setHasMore(data.hasMore);
        setTruncated((current) => page === 1 ? data.truncated : current || data.truncated);
        setNotices((current) => [...new Set([...(page === 1 ? [] : current), ...data.notices])]);
        setFetchedAt(data.fetchedAt);
      })
      .catch((reason: unknown) => {
        if (!controller.signal.aborted) setError(reason instanceof Error ? reason.message : "Could not load the discussion.");
      })
      .finally(() => { if (!controller.signal.aborted) setLoading(false); });
    return () => controller.abort();
  }, [providerId, itemId, page, attempt, generation]);

  const reload = () => {
    setEntries([]);
    setLoadedPage(0);
    setHasMore(false);
    setTruncated(false);
    setNotices([]);
    setFetchedAt("");
    setPage(1);
    setGeneration((value) => value + 1);
  };
  const entryIds = new Set(entries.map((entry) => entry.id));
  const localId = (id: string) => `discussion-entry-${id}`;

  return (
    <section className="discussion-section" aria-labelledby="discussion-heading" aria-busy={loading}>
      <div className="discussion-heading">
        <h3 id="discussion-heading">Discussion{loadedPage > 0 ? ` (${entries.length} loaded)` : ""}</h3>
        <button className="refresh-button" type="button" disabled={loading} onClick={reload}>Refresh discussion</button>
      </div>
      <p className="detail-format-note">{providerKind === "github" ? "General comments, submitted reviews and inline review comments." : "Notes, file discussions and system activity."} Shown as plain text. This is the current conversation; it may refer to commits other than the displayed diff.</p>
      <p className="discussion-coverage" role="status">
        {loadedPage > 0 && !loading && !error && !hasMore && !truncated ? "All requested pages loaded. " : "Discussion may be incomplete. "}
        Loaded entries are sorted oldest first. More pages may insert earlier entries.
        {fetchedAt && <> Last loaded at {new Date(fetchedAt).toLocaleTimeString()}.</>}
      </p>
      {(truncated || notices.length > 0) && (
        <div className="notice discussion-warning" role="status">
          <strong>Some discussion content is not shown in full.</strong>
          {notices.map((notice) => <p key={notice}>{notice}</p>)}
          <p>Limits: 10 pages, 100 messages and 512 KiB of text per page, 16 KiB per message.</p>
          <a href={providerURL} target="_blank" rel="noreferrer">Read the full discussion on {providerName} ↗</a>
        </div>
      )}
      {entries.length > 0 && (
        <ol className="discussion-list">
          {entries.map((entry) => (
            <li key={entry.id} id={localId(entry.id)} className="discussion-entry">
              <article>
                <header className="discussion-entry-header">
                  <strong>{entry.author || "Deleted user"}</strong>
                  <span className="discussion-kind">{kindLabels[entry.kind] ?? entry.kind}</span>
                  {entry.state && <span className="discussion-state">{stateLabels[entry.state] ?? entry.state}</span>}
                  {entry.resolved !== undefined && <span>{entry.resolved ? "Resolved" : "Unresolved"}</span>}
                  {!entry.createdAt.startsWith("0001-") ? <time dateTime={entry.createdAt}>{new Date(entry.createdAt).toLocaleString()}</time> : <span>Date unavailable</span>}
                  <a href={entry.htmlURL} target="_blank" rel="noreferrer">Open on {providerName} ↗</a>
                </header>
                {entry.replyTo && (
                  <p className="discussion-context">
                    {entryIds.has(entry.replyTo) ? <a href={`#${encodeURIComponent(localId(entry.replyTo))}`}>Reply to earlier comment</a> : <a href={entry.htmlURL} target="_blank" rel="noreferrer">Reply in a thread ↗</a>}
                  </p>
                )}
                {(entry.path || entry.commitSHA || entry.outdated) && (
                  <p className="discussion-context">
                    {entry.path && <><code>{entry.path}</code>{entry.line != null && <> · {entry.side === "LEFT" || entry.side === "old" ? "old" : "new"} line {entry.line}</>}</>}
                    {entry.commitSHA && <> · commit <code>{entry.commitSHA.slice(0, 12)}</code>{entry.commitSHA !== headSHA ? " (another revision)" : ""}</>}
                    {entry.outdated && <> · Outdated position</>}
                  </p>
                )}
                <div className="discussion-body" tabIndex={0}>{entry.body || (entry.bodyTruncated ? "Text omitted by the page budget." : "No comment body.")}</div>
                {entry.bodyTruncated && <p className="notice">Message shortened. <a href={entry.htmlURL} target="_blank" rel="noreferrer">Read the full message ↗</a></p>}
                {entry.updatedAt && !entry.updatedAt.startsWith("0001-") && Date.parse(entry.updatedAt) > Date.parse(entry.createdAt) && <p className="discussion-edited">Edited {new Date(entry.updatedAt).toLocaleString()}</p>}
              </article>
            </li>
          ))}
        </ol>
      )}
      {error && <div className="notice error-notice discussion-error" role="alert"><p>{error}</p><button className="refresh-button" type="button" onClick={() => setAttempt((value) => value + 1)}>Retry discussion</button></div>}
      {loading && <p role="status" className="detail-loading">Loading discussion…</p>}
      {!loading && !error && loadedPage > 0 && entries.length === 0 && <p>No published entries in the loaded pages.</p>}
      {!loading && !error && hasMore && <button className="refresh-button discussion-more" type="button" onClick={() => setPage(loadedPage + 1)}>Load more discussion</button>}
      <p className="detail-format-note"><a href={providerURL} target="_blank" rel="noreferrer">Continue the conversation on {providerName} ↗</a></p>
    </section>
  );
}
