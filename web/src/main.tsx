import React, { useCallback, useEffect, useRef, useState } from "react";
import { createRoot } from "react-dom/client";
import "./style.css";
import { request } from "./api";
import { DiffWorkspace } from "./DiffWorkspace";
import { DiscussionPanel } from "./DiscussionPanel";
import { ChecksPanel } from "./ChecksPanel";
import { ReviewDraft } from "./ReviewDraft";

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
  ci: CIState;
  reasons: string[];
};

type Detail = PullRequest & {
  description: string;
  revision: string;
  sourceBranch: string;
  targetBranch: string;
  headSHA: string;
  state: string;
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

type ProviderInfo = {
  id: string;
  name: string;
  kind: "github" | "gitlab";
  host: string;
  viewer: string;
};

type Session = {
  csrfToken: string;
  providers: ProviderInfo[];
  viewer: string;
  readOnly: boolean;
  mutationsAvailable: boolean;
  host: string;
};

async function bootstrap(): Promise<void> {
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
  }
}

function App() {
  const [session, setSession] = useState<Session | null>(null);
  const [csrfToken, setCsrfToken] = useState("");
  const [inboxes, setInboxes] = useState<Record<string, Inbox>>({});
  const [errors, setErrors] = useState<Record<string, string>>({});
  const [activeProvider, setActiveProvider] = useState("");
  const [activeTab, setActiveTab] = useState<"review" | "mine">("review");
  const [query, setQuery] = useState("");
  const [org, setOrg] = useState("");
  const [repo, setRepo] = useState("");
  const [order, setOrder] = useState<"oldest" | "recent">("oldest");
  const [loadingIds, setLoadingIds] = useState<string[]>([]);
  const [selectedPR, setSelectedPR] = useState<PullRequest | null>(null);
  const [startupError, setStartupError] = useState("");
  const [booting, setBooting] = useState(true);

  const loadInbox = useCallback(async (providerId: string, force = false, token = "") => {
    setErrors((current) => ({ ...current, [providerId]: "" }));
    setLoadingIds((current) => [...current, providerId]);
    const suffix = `?provider=${encodeURIComponent(providerId)}`;
    try {
      const data = await request<Inbox>(
        (force ? "/api/refresh" : "/api/inbox") + suffix,
        force ? { method: "POST" } : {},
        force ? token : undefined,
      );
      setInboxes((current) => ({ ...current, [providerId]: data }));
    } catch (reason) {
      const message = reason instanceof Error ? reason.message : "Could not load the inbox.";
      setErrors((current) => ({ ...current, [providerId]: message }));
    } finally {
      setLoadingIds((current) => current.filter((id) => id !== providerId));
    }
  }, []);

  useEffect(() => {
    let active = true;
    void (async () => {
      try {
        await bootstrap();
        if (!active) return;
        const identity = await request<Session>("/api/session");
        if (!active) return;
        setCsrfToken(identity.csrfToken);
        setSession(identity);
        const first = identity.providers[0]?.id ?? "";
        setActiveProvider(first);
        setBooting(false);
        await loadInbox(first);
      } catch (reason) {
        if (!active) return;
        setStartupError(reason instanceof Error ? reason.message : "Could not connect to PR Inbox.");
        setBooting(false);
      }
    })();
    return () => { active = false; };
  }, [loadInbox]);

  const providers = session?.providers ?? [];
  const provider = providers.find((item) => item.id === activeProvider);
  const inbox = inboxes[activeProvider] ?? null;
  const error = startupError || errors[activeProvider] || "";
  const loading = loadingIds.includes(activeProvider);
  const refreshing = loading && inbox !== null;
  const isGitLab = provider?.kind === "gitlab";
  const hostName = provider?.name ?? "GitHub";
  const noun = isGitLab ? "merge request" : "pull request";
  const nounPlural = isGitLab ? "merge requests" : "pull requests";

  const selectProvider = (id: string) => {
    setSelectedPR(null);
    setActiveProvider(id);
    setOrg("");
    setRepo("");
    setQuery("");
    if (!inboxes[id] && !loadingIds.includes(id)) void loadInbox(id, false, csrfToken);
  };

  const queue = activeTab === "review" ? inbox?.toReview : inbox?.myPRs;
  const queueItems = queue?.items ?? [];
  const orgOptions = countBy(queueItems, (item) => orgOf(item.repo));
  const activeOrg = orgOptions.some(([name]) => name === org) ? org : "";
  const repoOptions = countBy(
    queueItems.filter((item) => !activeOrg || orgOf(item.repo) === activeOrg),
    (item) => item.repo,
  );
  const activeRepo = repoOptions.some(([name]) => name === repo) ? repo : "";
  const hasFilters = query !== "" || activeOrg !== "" || activeRepo !== "";
  const filtered = queueItems
    .filter((item) => !activeOrg || orgOf(item.repo) === activeOrg)
    .filter((item) => !activeRepo || item.repo === activeRepo)
    .filter((item) => `${item.repo} ${item.title} ${item.author}`.toLowerCase().includes(query.toLowerCase()))
    .sort((left, right) => order === "recent"
      ? Date.parse(right.updated_at) - Date.parse(left.updated_at)
      : Date.parse(left.created_at) - Date.parse(right.created_at));

  if (booting && !startupError) {
    return <main className="center-state"><span className="spinner" />Connecting…</main>;
  }

  return (
    <div className="app-shell">
      <header className="topbar">
        <a className="brand" href="/" aria-label="PR Inbox home">
          <img className="brand-mark" src="/logo.svg" alt="" width="30" height="30" />
          <span className="brand-name"><span className="brand-pr">PR</span> Inbox</span>
        </a>
        {session && (
          <div className="account">
            {(provider?.viewer ?? session.viewer) ? (
              <>
                <span className="connection"><i /> {hostName} connected</span>
                <span className="account-login">@{provider?.viewer ?? session.viewer}</span>
              </>
            ) : (
              <span className="connection"><i /> {hostName} not signed in</span>
            )}
            {(session.readOnly || !session.mutationsAvailable) && <span className="mode-badge">Read only</span>}
          </div>
        )}
      </header>

      <main className="content">
        <section className="page-heading">
          <div>
            <p className="eyebrow">YOUR WORKSPACE</p>
            <h1>{isGitLab ? "Merge request inbox" : "Pull request inbox"}</h1>
            <p className="subheading">A clear view of what needs your attention.</p>
          </div>
          <button className="refresh-button" type="button" onClick={() => void loadInbox(activeProvider, true, csrfToken)} disabled={!session || loading}>
            <span aria-hidden="true" className={refreshing ? "refresh-icon spinning" : "refresh-icon"}>↻</span>
            {refreshing ? "Refreshing" : "Refresh"}
          </button>
        </section>

        {providers.length > 1 && (
          <nav className="providers" aria-label="Providers">
            {providers.map((item) => (
              <button
                key={item.id}
                className={item.id === activeProvider ? "provider active" : "provider"}
                onClick={() => selectProvider(item.id)}
                title={item.host}
              >
                {item.name}
              </button>
            ))}
          </nav>
        )}

        {error && (
          <section className="notice error-notice" role="alert">
            <strong>Inbox unavailable</strong>
            <p>{error}</p>
            {error.includes("gh auth login") && <code>gh auth login</code>}
            {isGitLab && error.includes("auth_required") && <code>glab auth login --hostname {provider?.host}</code>}
          </section>
        )}

        {loading && !inbox && !error && (
          <section className="loading-panel" role="status" aria-live="polite">
            <div className="loading-bar" aria-hidden="true"><i /></div>
            <strong>Loading your {nounPlural}…</strong>
            <p>Searching {hostName} for {nounPlural} that need your review and the ones you authored. This can take a few seconds.</p>
          </section>
        )}

        {inbox && (
          <>
            <div className="freshness">
              <span><i className="fresh-dot" /> Last updated {new Date(inbox.fetchedAt).toLocaleTimeString([], { hour: "numeric", minute: "2-digit" })}</span>
              <span className="coverage">{isGitLab ? inbox.teamCoverage.replace("team-request", "group-request") : inbox.teamCoverage}</span>
            </div>
            <nav className="tabs" aria-label="Queues">
              <button className={activeTab === "review" ? "tab active" : "tab"} onClick={() => setActiveTab("review")}>
                To review <span className="count">{inbox.toReview.totalCount}</span>
              </button>
              <button className={activeTab === "mine" ? "tab active" : "tab"} onClick={() => setActiveTab("mine")}>
                {isGitLab ? "My MRs" : "My PRs"} <span className="count">{inbox.myPRs.totalCount}</span>
              </button>
            </nav>

            <section className="queue-section">
              <div className="queue-heading">
                <div>
                  <h2>{activeTab === "review" ? "Waiting for your review" : "Authored by you"}</h2>
                  <p>{isGitLab ? queue?.ordering.replace("PR", "merge request") : queue?.ordering}</p>
                </div>
                <div className="queue-controls">
                  <label className="visually-hidden" htmlFor="org-filter">Filter by organization</label>
                  <select
                    id="org-filter"
                    value={activeOrg}
                    onChange={(event) => { setOrg(event.target.value); setRepo(""); }}
                  >
                    <option value="">All organizations</option>
                    {orgOptions.map(([name, count]) => <option key={name} value={name}>{name} ({count})</option>)}
                  </select>
                  <label className="visually-hidden" htmlFor="repo-filter">Filter by repository</label>
                  <select id="repo-filter" value={activeRepo} onChange={(event) => setRepo(event.target.value)}>
                    <option value="">All repositories</option>
                    {repoOptions.map(([name, count]) => (
                      <option key={name} value={name}>{activeOrg ? name.slice(activeOrg.length + 1) : name} ({count})</option>
                    ))}
                  </select>
                  <label className="visually-hidden" htmlFor="sort-order">Sort {nounPlural}</label>
                  <select id="sort-order" value={order} onChange={(event) => setOrder(event.target.value as "oldest" | "recent")}>
                    <option value="oldest">Oldest first</option>
                    <option value="recent">Recently updated</option>
                  </select>
                  <label className="visually-hidden" htmlFor="filter">Filter {nounPlural}</label>
                  <input id="filter" value={query} onChange={(event) => setQuery(event.target.value)} placeholder="Filter inbox" />
                </div>
              </div>

              {queue?.truncated && (
                <div className="notice truncation-notice" role="status">
                  <strong>Not all {nounPlural} are shown.</strong>{" "}
                  This queue has {queue.totalCount} matching {nounPlural}, but only the first {queue.items.length} are loaded
                  (limit of {queue.items.length} per queue). Some {nounPlural}, including possibly older ones, may be missing.
                  {isGitLab
                    ? <>The group and project filters above only filter what is already loaded.</>
                    : <>Restart with <code>--org ORG</code> to narrow discovery to the organizations you care about.
                      The organization and repository filters above only filter what is already loaded.</>}
                </div>
              )}
              {filtered.length === 0 ? (
                <div className="empty-state">
                  <div className="empty-icon" aria-hidden="true">{hasFilters ? "⌕" : "✓"}</div>
                  <h3>{hasFilters ? `No matching ${nounPlural}` : "You're all caught up"}</h3>
                  <p>{hasFilters ? "Try different filters, or clear the organization and repository selection." : `There are no open ${nounPlural} in this queue right now.`}</p>
                </div>
              ) : (
                <ul className="pr-list">
                  {filtered.map((item) => <PRCard key={item.id} item={item} noun={noun} hostName={hostName} onSelect={() => setSelectedPR(item)} />)}
                </ul>
              )}
            </section>
          </>
        )}
      </main>
      {selectedPR && provider && (
        <DetailPanel key={`${provider.id}:${selectedPR.id}`} item={selectedPR} provider={provider} csrfToken={csrfToken} readOnly={session?.readOnly ?? true} onClose={() => setSelectedPR(null)} />
      )}
      <footer className="footer">Local to this device · No PR code is executed</footer>
    </div>
  );
}

function DetailPanel({ item, provider, csrfToken, readOnly, onClose }: { item: PullRequest; provider: ProviderInfo; csrfToken: string; readOnly: boolean; onClose: () => void }) {
  const dialog = useRef<HTMLDialogElement>(null);
  const [detail, setDetail] = useState<Detail | null>(null);
  const [error, setError] = useState("");
  const [attempt, setAttempt] = useState(0);
  const draftDirty = useRef(false);
  const onDraftDirty = useCallback((dirty: boolean) => { draftDirty.current = dirty; }, []);
  function confirmLeavingDraft() { return !draftDirty.current || window.confirm("Leave without saving your draft changes? Use Save draft to keep them."); }
  function closeDetails() { if (confirmLeavingDraft()) onClose(); }
  function reloadDetails() { if (confirmLeavingDraft()) setAttempt((value) => value + 1); }
  const isGitLab = provider.kind === "gitlab";

  useEffect(() => {
    const element = dialog.current;
    const previousFocus = document.activeElement;
    element?.showModal();
    return () => {
      element?.close();
      if (previousFocus instanceof HTMLElement && previousFocus.isConnected) previousFocus.focus();
    };
  }, []);

  useEffect(() => {
    const controller = new AbortController();
    setDetail(null);
    setError("");
    void request<Detail>(`/api/detail?provider=${encodeURIComponent(provider.id)}&id=${item.id}`, { signal: controller.signal })
      .then((data) => { if (!controller.signal.aborted) setDetail(data); })
      .catch((reason: unknown) => {
        if (!controller.signal.aborted) setError(reason instanceof Error ? reason.message : "Could not load details.");
      });
    return () => controller.abort();
  }, [provider.id, item.id, attempt]);

  const current = detail ?? item;
  return (
    <dialog className="detail-dialog" ref={dialog} aria-labelledby="detail-title" onCancel={(event) => { event.preventDefault(); closeDetails(); }} onClose={onClose}>
      <div className="detail-toolbar">
        <span>{item.repo} {isGitLab ? "!" : "#"}{item.number}</span>
        <button type="button" className="refresh-button" onClick={closeDetails} autoFocus>Close</button>
      </div>
      <h2 id="detail-title">{current.title}</h2>
      <p className="detail-author">Opened by <strong>{current.author}</strong></p>
      <a className="detail-provider-link" href={item.html_url} target="_blank" rel="noreferrer">Open on {provider.name} ↗</a>
      {error ? (
        <div className="notice error-notice" role="alert">
          <p>{error}</p>
          <button className="refresh-button" type="button" onClick={() => setAttempt((value) => value + 1)}>Retry</button>
        </div>
      ) : !detail ? (
        <p className="detail-loading" role="status">Loading details…</p>
      ) : (
        <>
          <dl className="detail-metadata">
            <div><dt>State</dt><dd>{detail.state}{detail.draft ? " · Draft" : ""}</dd></div>
            <div><dt>Branches</dt><dd><code>{detail.sourceBranch}</code> → <code>{detail.targetBranch}</code></dd></div>
            <div><dt>Head commit</dt><dd><code>{detail.headSHA}</code></dd></div>
          </dl>
          <section className="detail-description" aria-label="Description">
            <h3>Description</h3>
            <p className="detail-format-note">Shown as plain text.</p>
            <div className="description-text">{detail.description || "No description provided."}</div>
          </section>
          <ReviewDraft key={`draft:${detail.revision}`} providerId={provider.id} itemId={item.id} revision={detail.revision} headSHA={detail.headSHA} csrfToken={csrfToken} readOnly={readOnly} onDirtyChange={onDraftDirty} />
          <ChecksPanel key={`checks:${detail.revision}`} providerId={provider.id} itemId={item.id} revision={detail.revision} headSHA={detail.headSHA} providerName={provider.name} providerKind={provider.kind} providerURL={item.html_url} />
          <DiffWorkspace csrfToken={csrfToken} readOnly={readOnly} key={detail.revision} providerId={provider.id} itemId={item.id} revision={detail.revision} headSHA={detail.headSHA} providerName={provider.name} providerURL={item.html_url} onReload={reloadDetails} />
          <DiscussionPanel key={`discussion:${detail.revision}`} providerId={provider.id} itemId={item.id} providerName={provider.name} providerKind={provider.kind} providerURL={item.html_url} headSHA={detail.headSHA} />
        </>
      )}
    </dialog>
  );
}

function orgOf(repo: string): string {
  return repo.split("/")[0];
}

function countBy(items: PullRequest[], key: (item: PullRequest) => string): [string, number][] {
  const counts = new Map<string, number>();
  for (const item of items) counts.set(key(item), (counts.get(key(item)) ?? 0) + 1);
  return [...counts.entries()].sort((left, right) => left[0].localeCompare(right[0]));
}

type CIState = "success" | "failure" | "pending" | "none" | "unknown";

const ciLabels: Record<CIState, { text: string; help: string }> = {
  success: { text: "CI passing", help: "All checks on the latest commit passed." },
  failure: { text: "CI failing", help: "At least one check on the latest commit failed." },
  pending: { text: "CI running", help: "Checks on the latest commit are still running or pending." },
  none: { text: "No CI", help: "No checks or statuses were reported for the latest commit." },
  unknown: { text: "CI unknown", help: "The CI status could not be loaded from the provider." },
};

const reasonHelp: Record<string, string> = {
  "Requested from you": "You were directly asked to review this pull request.",
  "Authored by you": "You opened this pull request.",
};

function PRCard({ item, noun, hostName, onSelect }: { item: PullRequest; noun: string; hostName: string; onSelect: () => void }) {
  const draft = item.draft === true ? "Draft" : item.draft === false ? "Ready for review" : "Draft status unavailable";
  return (
    <li className="pr-card">
      <div className="pr-main">
        <div className="repo-line">
          <span className="repo-icon" aria-hidden="true">⌘</span>
          <span>{item.repo}</span>
          <span className="separator">/</span>
          <button className="pr-title" type="button" onClick={onSelect}>{item.title}</button>
          <span className="pr-number">#{item.number}</span>
        </div>
        <p className="pr-meta">Opened by <strong>{item.author}</strong> · {new Date(item.created_at).toLocaleDateString()}</p>
      </div>
      <div className="pr-details">
        <span className={item.draft === true ? "draft-label" : "state-label"}>{draft}</span>
        <span className={`ci-label ci-${item.ci ?? "unknown"}`} title={ciLabels[item.ci ?? "unknown"].help}>
          {ciLabels[item.ci ?? "unknown"].text}
        </span>
        {item.reasons.map((reason) => (
          <span className="reason-label" key={reason} title={reasonHelp[reason]}>{reason}</span>
        ))}
      </div>
      <a className="open-link" href={item.html_url} target="_blank" rel="noreferrer" aria-label={`Open ${item.repo} ${noun} ${item.number} on ${hostName}`}>↗</a>
    </li>
  );
}

createRoot(document.getElementById("root")!).render(
  <React.StrictMode><App /></React.StrictMode>,
);
