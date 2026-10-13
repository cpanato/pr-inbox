import React, { useCallback, useEffect, useState } from "react";
import { createRoot } from "react-dom/client";
import "./style.css";

type PullRequest = {
  id: number;
  number: number;
  title: string;
  html_url: string;
  repo: string;
  author: string;
  created_at: string;
  updated_at: string;
  draft: boolean | null;
  reasons: string[];
};

type Queue = {
  items: PullRequest[];
  totalCount: number;
  truncated: boolean;
  ordering: string;
};

type Inbox = {
  fetchedAt: string;
  viewer: string;
  toReview: Queue;
  myPRs: Queue;
  teamCoverage: string;
};

type Session = {
  viewer: string;
  readOnly: boolean;
  mutationsAvailable: boolean;
  host: string;
};

type APIError = Error & { code?: string };

async function request<T>(
  path: string,
  options: RequestInit = {},
  csrfToken?: string,
): Promise<T> {
  const headers = new Headers(options.headers);
  if (csrfToken) headers.set("X-CSRF-Token", csrfToken);
  if (options.body) headers.set("Content-Type", "application/json");
  const response = await fetch(path, {
    ...options,
    headers,
    credentials: "same-origin",
  });
  const payload = await response.json();
  if (!response.ok) {
    const error = new Error(payload.message ?? "The request failed.") as APIError;
    error.code = payload.error;
    throw error;
  }
  return payload as T;
}

async function bootstrap(): Promise<string> {
  const parameters = new URLSearchParams(window.location.hash.slice(1));
  const launchKey = parameters.get("launch");
  if (launchKey) {
    window.history.replaceState(null, "", window.location.pathname + window.location.search);
    const response = await fetch("/api/bootstrap", {
      method: "POST",
      headers: { "X-Launch-Key": launchKey },
      credentials: "same-origin",
    });
    const payload = await response.json();
    if (!response.ok) throw new Error(payload.message ?? "Could not open the local session.");
    return payload.csrfToken as string;
  }
  return "";
}

function App() {
  const [session, setSession] = useState<Session | null>(null);
  const [csrfToken, setCsrfToken] = useState("");
  const [inbox, setInbox] = useState<Inbox | null>(null);
  const [activeTab, setActiveTab] = useState<"review" | "mine">("review");
  const [query, setQuery] = useState("");
  const [order, setOrder] = useState<"oldest" | "recent">("oldest");
  const [loading, setLoading] = useState(true);
  const [refreshing, setRefreshing] = useState(false);
  const [error, setError] = useState("");

  const loadInbox = useCallback(async (force = false, token = "") => {
    setError("");
    setRefreshing(force);
    try {
      const data = await request<Inbox>(
        force ? "/api/refresh" : "/api/inbox",
        force ? { method: "POST" } : {},
        force ? token : undefined,
      );
      setInbox(data);
    } catch (reason) {
      const message = reason instanceof Error ? reason.message : "Could not load the inbox.";
      setError(message);
    } finally {
      setLoading(false);
      setRefreshing(false);
    }
  }, []);

  useEffect(() => {
    let active = true;
    void (async () => {
      try {
        const token = await bootstrap();
        if (!active) return;
        setCsrfToken(token);
        const identity = await request<Session>("/api/session");
        if (!active) return;
        setSession(identity);
        await loadInbox(false, token);
      } catch (reason) {
        if (!active) return;
        setError(reason instanceof Error ? reason.message : "Could not connect to PR Inbox.");
        setLoading(false);
      }
    })();
    return () => { active = false; };
  }, [loadInbox]);

  const queue = activeTab === "review" ? inbox?.toReview : inbox?.myPRs;
  const filtered = (queue?.items ?? [])
    .filter((item) => `${item.repo} ${item.title} ${item.author}`.toLowerCase().includes(query.toLowerCase()))
    .sort((left, right) => order === "recent"
      ? Date.parse(right.updated_at) - Date.parse(left.updated_at)
      : Date.parse(left.created_at) - Date.parse(right.created_at));

  if (loading && !session && !error) {
    return <main className="center-state"><span className="spinner" />Connecting to GitHub…</main>;
  }

  return (
    <div className="app-shell">
      <header className="topbar">
        <a className="brand" href="/" aria-label="PR Inbox home">
          <span className="brand-mark" aria-hidden="true">PR</span>
          <span>inbox</span>
        </a>
        {session && (
          <div className="account">
            <span className="connection"><i /> GitHub connected</span>
            <span className="account-login">@{session.viewer}</span>
            {(session.readOnly || !session.mutationsAvailable) && <span className="mode-badge">Read only</span>}
          </div>
        )}
      </header>

      <main className="content">
        <section className="page-heading">
          <div>
            <p className="eyebrow">YOUR WORKSPACE</p>
            <h1>Pull request inbox</h1>
            <p className="subheading">A clear view of what needs your attention.</p>
          </div>
          <button className="refresh-button" type="button" onClick={() => void loadInbox(true)} disabled={!session || refreshing}>
            <span aria-hidden="true" className={refreshing ? "refresh-icon spinning" : "refresh-icon"}>↻</span>
            {refreshing ? "Refreshing" : "Refresh"}
          </button>
        </section>

        {error && (
          <section className="notice error-notice" role="alert">
            <strong>Inbox unavailable</strong>
            <p>{error}</p>
            {error.includes("gh auth login") && <code>gh auth login</code>}
          </section>
        )}

        {inbox && (
          <>
            <div className="freshness">
              <span><i className="fresh-dot" /> Last updated {new Date(inbox.fetchedAt).toLocaleTimeString([], { hour: "numeric", minute: "2-digit" })}</span>
              <span className="coverage">{inbox.teamCoverage}</span>
            </div>
            <nav className="tabs" aria-label="Pull request queues">
              <button className={activeTab === "review" ? "tab active" : "tab"} onClick={() => setActiveTab("review")}>
                To review <span className="count">{inbox.toReview.totalCount}</span>
              </button>
              <button className={activeTab === "mine" ? "tab active" : "tab"} onClick={() => setActiveTab("mine")}>
                My PRs <span className="count">{inbox.myPRs.totalCount}</span>
              </button>
            </nav>

            <section className="queue-section">
              <div className="queue-heading">
                <div>
                  <h2>{activeTab === "review" ? "Waiting for your review" : "Authored by you"}</h2>
                  <p>{queue?.ordering}</p>
                </div>
                <div className="queue-controls">
                  <label className="visually-hidden" htmlFor="sort-order">Sort pull requests</label>
                  <select id="sort-order" value={order} onChange={(event) => setOrder(event.target.value as "oldest" | "recent")}>
                    <option value="oldest">Oldest first</option>
                    <option value="recent">Recently updated</option>
                  </select>
                  <label className="visually-hidden" htmlFor="filter">Filter pull requests</label>
                  <input id="filter" value={query} onChange={(event) => setQuery(event.target.value)} placeholder="Filter inbox" />
                </div>
              </div>

              {queue?.truncated && (
                <div className="notice truncation-notice">
                  Showing a limited result set. Add an organization filter with <code>--org ORG</code> for narrower discovery.
                </div>
              )}
              {filtered.length === 0 ? (
                <div className="empty-state">
                  <div className="empty-icon" aria-hidden="true">{query ? "⌕" : "✓"}</div>
                  <h3>{query ? "No matching pull requests" : "You're all caught up"}</h3>
                  <p>{query ? "Try a different title, repository, or author." : "There are no open pull requests in this queue right now."}</p>
                </div>
              ) : (
                <ul className="pr-list">
                  {filtered.map((item) => <PRCard key={item.id} item={item} />)}
                </ul>
              )}
            </section>
          </>
        )}
      </main>
      <footer className="footer">Local to this device · No PR code is executed</footer>
    </div>
  );
}

function PRCard({ item }: { item: PullRequest }) {
  const draft = item.draft === true ? "Draft" : item.draft === false ? "Ready for review" : "Draft status unavailable";
  return (
    <li className="pr-card">
      <div className="pr-main">
        <div className="repo-line">
          <span className="repo-icon" aria-hidden="true">⌘</span>
          <span>{item.repo}</span>
          <span className="separator">/</span>
          <a href={item.html_url} target="_blank" rel="noreferrer">{item.title}</a>
          <span className="pr-number">#{item.number}</span>
        </div>
        <p className="pr-meta">Opened by <strong>{item.author}</strong> · {new Date(item.created_at).toLocaleDateString()}</p>
      </div>
      <div className="pr-details">
        <span className={item.draft === true ? "draft-label" : "state-label"}>{draft}</span>
        <span className="check-label" title="Check summaries will be added in the detail milestone">Checks not loaded</span>
        {item.reasons.map((reason) => <span className="reason-label" key={reason}>{reason}</span>)}
      </div>
      <a className="open-link" href={item.html_url} target="_blank" rel="noreferrer" aria-label={`Open ${item.repo} pull request ${item.number} on GitHub`}>↗</a>
    </li>
  );
}

createRoot(document.getElementById("root")!).render(
  <React.StrictMode><App /></React.StrictMode>,
);
