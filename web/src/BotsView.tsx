import React, { useCallback, useEffect, useMemo, useRef, useState } from "react";
import { request } from "./api";

type BotPR = {
  id: number;
  number: number;
  title: string;
  html_url: string;
  repo: string;
  author: string;
  created_at: string;
  ci: string;
  headSHA: string;
  mergeable: string;
  review: string;
  branch: string;
  baseBranch: string;
};
type JobItem = { id: number; repo: string; number: number; status: string; error?: string };
type Job = { id: string; action: string; items: JobItem[]; finished: boolean };
type RowStatus = { status: string; error?: string };
type RepoIndex = { repos: { repo: string; count: number }[]; total: number; incomplete: boolean; fetchedAt: string };
// Heavy details (CI, conflicts, reviews) load this many repositories at a time, which keeps huge orgs fast.
const defaultPageSize = 5;
const pageSizeOptions = [5, 10, 20, 50];
const rowStatusText: Record<string, string> = { queued: "Queued", running: "Working…", failed: "Failed" };
const filterRepos = (index: RepoIndex, search: string, picked: Set<string>) => index.repos.filter((item) => (picked.size === 0 || picked.has(item.repo)) && item.repo.toLowerCase().includes(search.trim().toLowerCase())).map((item) => item.repo);
const sleep = (ms: number) => new Promise((resolve) => window.setTimeout(resolve, ms));

type Listing = { org: string; author: string; items: BotPR[]; totalCount: number; incomplete: boolean; fetchedAt: string; mergeMethods: string[] };
type ProviderInfo = { id: string; name: string; kind: string; viewer: string; host?: string; botsAvailable?: boolean };
type Props = { providers: ProviderInfo[]; csrfToken: string; readOnly: boolean };

const authorPresets = ["dependabot", "renovate"];
const statusFilters = [
  ["all", "All"],
  ["ready", "Ready to merge"],
  ["conflicts", "Has conflicts"],
  ["failing", "CI failing"],
  ["pending", "CI running"],
] as const;
const ciText: Record<string, string> = { success: "CI passing", failure: "CI failing", pending: "CI running", none: "No CI", unknown: "CI unknown" };
const mergeText: Record<string, string> = { clean: "Mergeable", conflicting: "Conflicts", blocked: "Blocked", behind: "Behind base", unknown: "Checking…" };
// The PR URL minus its /pull/N or /-/merge_requests/N tail is the repository page.
const repoURL = (prURL: string) => prURL.replace(/\/(?:-\/)?(?:pull|merge_requests)\/\d+.*$/, "");

// Recomputes the total from per-repo counts. Emptied repos stay in the list so page boundaries do not shift.
const withCounts = (index: RepoIndex, counts: Map<string, number>): RepoIndex => {
  const repos = index.repos.map((item) => (counts.has(item.repo) ? { ...item, count: counts.get(item.repo)! } : item));
  return { ...index, repos, total: repos.reduce((sum, item) => sum + item.count, 0) };
};

const reviewText: Record<string, string> = { approved: "Approved", changes_requested: "Changes requested", review_required: "Review required", none: "No review" };

const storageGet = (key: string, fallback: string) => { try { return window.localStorage.getItem(key) ?? fallback; } catch { return fallback; } };
const storageSet = (key: string, value: string) => { try { window.localStorage.setItem(key, value); } catch { /* storage unavailable */ } };

const otherOrg = "__other__";

export function BotsView({ providers, csrfToken, readOnly }: Props) {
  const available = providers.filter((item) => item.botsAvailable);
  const [providerId, setProviderId] = useState(available[0]?.id ?? "");
  const [orgs, setOrgs] = useState<string[]>([]);
  const [org, setOrg] = useState(() => storageGet("bots.org", ""));
  const [customOrg, setCustomOrg] = useState(false);
  const [author, setAuthor] = useState(() => storageGet("bots.author", "dependabot"));
  const [label, setLabel] = useState(() => storageGet("bots.label", ""));
  const [listing, setListing] = useState<Listing | null>(null);
  const [loading, setLoading] = useState(false);
  const [paging, setPaging] = useState(false);
  const [index, setIndex] = useState<RepoIndex | null>(null);
  const [page, setPage] = useState(0);
  const [rowStatus, setRowStatus] = useState<Map<number, RowStatus>>(new Map());
  const [activeJobs, setActiveJobs] = useState<string[]>([]);
  const listingRef = useRef<Listing | null>(null);
  const indexRef = useRef<RepoIndex | null>(null);
  const queryRef = useRef("");
  const pageRef = useRef(0);
  const [pageSize, setPageSize] = useState(() => { const saved = Number(storageGet("bots.pageSize", "")); return pageSizeOptions.includes(saved) ? saved : defaultPageSize; });
  const [pickedRepos, setPickedRepos] = useState<Set<string>>(new Set());
  const pageSizeRef = useRef(pageSize);
  const pickedRef = useRef(pickedRepos);
  // Only the newest load may update the screen, so a slow or failed request
  // for a previous organization can never overwrite or block the current one.
  const loadSeq = useRef(0);
  const contextRef = useRef("");
  const alive = useRef(true);
  const [error, setError] = useState("");
  const [notice, setNotice] = useState("");
  const [selected, setSelected] = useState<Set<number>>(new Set());
  const [status, setStatus] = useState<string>("all");
  const [query, setQuery] = useState("");
  const [method, setMethod] = useState("");
  const [comment, setComment] = useState("");
  const [confirmMerge, setConfirmMerge] = useState(false);
  const [confirmClose, setConfirmClose] = useState(false);
  const [refreshing, setRefreshing] = useState<Set<string>>(new Set());
  const [expanded, setExpanded] = useState<Set<string>>(new Set());
  const [customFor, setCustomFor] = useState<number | null>(null);
  const [customText, setCustomText] = useState("");

  const load = useCallback(async (nextOrg: string, nextAuthor: string, keep = false) => {
    if (!nextOrg.trim() || (!nextAuthor.trim() && !label.trim())) return;
    const seq = ++loadSeq.current;
    const stale = () => seq !== loadSeq.current;
    setLoading(true);
    setPaging(false);
    setError("");
    if (!keep) { setListing(null); setIndex(null); setSelected(new Set()); }
    try {
      const base = { provider: providerId, org: nextOrg.trim(), author: nextAuthor.trim(), label: label.trim() };
      const data = await request<RepoIndex>(`/api/bots/repos?${new URLSearchParams(base)}`);
      if (stale()) return;
      const fresh = { ...data, repos: data.repos ?? [] };
      setIndex(fresh);
      const names = filterRepos(fresh, queryRef.current, pickedRef.current);
      const size = pageSizeRef.current;
      const pageNo = keep ? Math.min(pageRef.current, Math.max(0, Math.ceil(names.length / size) - 1)) : 0;
      setPage(pageNo);
      await fetchPage(names.slice(pageNo * size, (pageNo + 1) * size), base, keep, stale);
      if (stale()) return;
      storageSet("bots.org", nextOrg.trim());
      storageSet("bots.author", nextAuthor.trim());
      storageSet("bots.label", label.trim());
    } catch (reason) {
      if (!stale()) setError(reason instanceof Error ? reason.message : "Could not load pull requests.");
    } finally {
      if (!stale()) setLoading(false);
    }
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [providerId, label]);

  const fetchPage = async (names: string[], base: { provider: string; org: string; author: string; label: string }, keep: boolean, stale: () => boolean) => {
    let data: Listing = { org: base.org, author: base.author, items: [], totalCount: 0, incomplete: false, fetchedAt: new Date().toISOString(), mergeMethods: listingRef.current?.mergeMethods ?? [] };
    if (names.length > 0) {
      const params = new URLSearchParams(base);
      names.forEach((name) => params.append("repo", name));
      data = await request<Listing>(`/api/bots?${params}`);
    }
    if (stale()) return;
    setListing({ ...data, items: data.items ?? [], mergeMethods: data.mergeMethods ?? [] });
    setMethod((current) => ((data.mergeMethods ?? []).includes(current) ? current : (data.mergeMethods ?? [])[0] ?? ""));
    setSelected((current) => {
      const present = new Set((data.items ?? []).map((item) => item.id));
      return keep ? new Set([...current].filter((id) => present.has(id))) : new Set();
    });
  };

  const goToPage = async (pageNo: number, source = index, search = query, size = pageSizeRef.current, picked = pickedRef.current) => {
    if (!source || loading) return;
    const names = filterRepos(source, search, picked);
    const bounded = Math.max(0, Math.min(pageNo, Math.ceil(names.length / size) - 1));
    const seq = ++loadSeq.current;
    const stale = () => seq !== loadSeq.current;
    setLoading(true);
    setPaging(true);
    setError("");
    try {
      await fetchPage(names.slice(bounded * size, (bounded + 1) * size), { provider: providerId, org: org.trim(), author: author.trim(), label: label.trim() }, false, stale);
      if (!stale()) setPage(bounded);
    } catch (reason) {
      if (!stale()) setError(reason instanceof Error ? reason.message : "Could not load this page.");
    } finally {
      if (!stale()) { setLoading(false); setPaging(false); }
    }
  };

  // Typing in search narrows the repository list; wait for a pause before loading.
  useEffect(() => {
    if (!indexRef.current) return;
    const timer = window.setTimeout(() => void goToPage(0, indexRef.current, query), 400);
    return () => window.clearTimeout(timer);
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [query]);

  const changePageSize = (size: number) => {
    setPageSize(size);
    storageSet("bots.pageSize", String(size));
    void goToPage(0, index, query, size);
  };

  const changePicked = (next: Set<string>) => {
    setPickedRepos(next);
    void goToPage(0, index, query, pageSize, next);
  };

  useEffect(() => {
    if (!providerId) return;
    setListing(null);
    setIndex(null);
    setOrgs([]);
    void request<{ orgs: string[] }>(`/api/bots/orgs?provider=${encodeURIComponent(providerId)}`)
      .then((data) => {
        const names = data.orgs ?? [];
        setOrgs(names);
        const initial = org || names[0] || "";
        if (initial) { setOrg(initial); void load(initial, author); }
      })
      .catch(() => { if (org) void load(org, author); });
    // Only reload when the provider changes.
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [providerId]);

  listingRef.current = listing;
  indexRef.current = index;
  queryRef.current = query;
  pageSizeRef.current = pageSize;
  pickedRef.current = pickedRepos;
  pageRef.current = page;
  contextRef.current = `${providerId}|${org.trim()}|${author.trim()}|${label.trim()}`;
  useEffect(() => () => { alive.current = false; }, []);

  const visible = useMemo(() => (listing?.items ?? []).filter((item) => {
    if (status === "ready" && !(item.mergeable === "clean" && item.ci === "success")) return false;
    if (status === "conflicts" && item.mergeable !== "conflicting") return false;
    if (status === "failing" && item.ci !== "failure") return false;
    if (status === "pending" && item.ci !== "pending") return false;
    return `${item.repo} ${item.title}`.toLowerCase().includes(query.toLowerCase());
  }), [listing, status, query]);
  const matching = useMemo(() => (index ? filterRepos(index, query, pickedRepos) : []), [index, query, pickedRepos]);
  const groups = useMemo(() => {
    const map = new Map<string, BotPR[]>();
    for (const item of visible) map.set(item.repo, [...(map.get(item.repo) ?? []), item]);
    return [...map.entries()];
  }, [visible]);
  const refreshRepo = async (repo: string) => {
    if (refreshing.has(repo)) return;
    setRefreshing((current) => new Set(current).add(repo));
    try {
      const params = new URLSearchParams({ provider: providerId, org: org.trim(), author: author.trim(), label: label.trim(), repo });
      const data = await request<Listing>(`/api/bots?${params}`);
      const fresh = data.items ?? [];
      setIndex((current) => (current ? withCounts(current, new Map([[repo, fresh.length]])) : current));
      setListing((current) => {
        if (!current) return current;
        const others = current.items.filter((item) => item.repo !== repo);
        const before = current.items.length - others.length;
        const items = [...others, ...fresh].sort((a, b) => a.repo.localeCompare(b.repo) || a.created_at.localeCompare(b.created_at));
        return { ...current, items, totalCount: Math.max(items.length, current.totalCount - before + fresh.length), fetchedAt: data.fetchedAt };
      });
      const present = new Set(fresh.map((item) => item.id));
      const old = new Set((listingRef.current?.items ?? []).filter((item) => item.repo === repo).map((item) => item.id));
      setSelected((current) => new Set([...current].filter((id) => present.has(id) || !old.has(id))));
    } catch (reason) {
      setError(reason instanceof Error ? reason.message : `Could not refresh ${repo}.`);
    } finally {
      setRefreshing((current) => { const next = new Set(current); next.delete(repo); return next; });
    }
  };

  const toggleExpanded = (repo: string) => setExpanded((current) => {
    const next = new Set(current);
    if (!next.delete(repo)) next.add(repo);
    return next;
  });
  const allCollapsed = groups.length > 0 && groups.every(([repo]) => !expanded.has(repo));
  const targets = visible.filter((item) => selected.has(item.id));

  const toggle = (ids: number[], on: boolean) => setSelected((current) => {
    const next = new Set(current);
    for (const id of ids) on ? next.add(id) : next.delete(id);
    return next;
  });

  const setStatuses = (updates: [number, RowStatus | null][]) => setRowStatus((current) => {
    const next = new Map(current);
    for (const [id, value] of updates) value ? next.set(id, value) : next.delete(id);
    return next;
  });
  const isActive = (id: number) => { const state = rowStatus.get(id)?.status; return state === "queued" || state === "running"; };

  // Actions run in a server-side job; this polls it and updates only the rows and repos it touches.
  const run = async (action: "approve" | "merge" | "close" | "comment", items: BotPR[], body = "") => {
    const fresh = items.filter((item) => !isActive(item.id));
    if (fresh.length === 0) return;
    const context = contextRef.current;
    setNotice("");
    setConfirmMerge(false);
    setConfirmClose(false);
    setStatuses(fresh.map((item) => [item.id, { status: "queued" }]));
    let job: Job;
    try {
      job = await request<Job>("/api/bots/jobs", { method: "POST", body: JSON.stringify({ provider: providerId, ids: fresh.map((item) => item.id), action, method, body }) }, csrfToken);
    } catch (reason) {
      setStatuses(fresh.map((item) => [item.id, null]));
      setError(reason instanceof Error ? reason.message : "Could not start the action.");
      return;
    }
    setActiveJobs((current) => [...current, job.id]);
    const applied = new Set<number>();
    const refreshed = new Set<string>();
    const repoItems = (repo: string) => job.items.filter((item) => item.repo === repo);
    try {
      for (;;) {
        const finished = job.finished;
        const done = job.items.filter((item) => item.status === "done" && !applied.has(item.id));
        done.forEach((item) => applied.add(item.id));
        if (done.length > 0 && (action === "merge" || action === "close")) {
          const ids = new Set(done.map((item) => item.id));
          setListing((current) => (current ? { ...current, items: current.items.filter((item) => !ids.has(item.id)), totalCount: Math.max(0, current.totalCount - ids.size) } : current));
          const gone = new Map<string, number>();
          for (const item of listingRef.current?.items ?? []) if (ids.has(item.id)) gone.set(item.repo, (gone.get(item.repo) ?? 0) + 1);
          setIndex((current) => (current ? withCounts(current, new Map([...gone].map(([repo, count]) => [repo, (current.repos.find((item) => item.repo === repo)?.count ?? 0) - count]))) : current));
          setSelected((current) => new Set([...current].filter((id) => !ids.has(id))));
        }
        if (done.length > 0 && action === "approve") {
          const ids = new Set(done.map((item) => item.id));
          setListing((current) => (current ? { ...current, items: current.items.map((item) => (ids.has(item.id) ? { ...item, review: "approved" } : item)) } : current));
        }
        setStatuses(job.items.map((item): [number, RowStatus | null] => [item.id, item.status === "done" || item.status === "cancelled" ? null : { status: item.status, error: item.error }]));
        if (!alive.current || contextRef.current !== context) break;
        for (const repo of new Set(job.items.map((item) => item.repo))) {
          if (!refreshed.has(repo) && repoItems(repo).every((item) => item.status !== "queued" && item.status !== "running")) {
            refreshed.add(repo);
            void refreshRepo(repo);
            // Dependabot reacts to rebase and recreate comments after a delay.
            if (action === "comment") for (const delay of [10000, 30000]) window.setTimeout(() => { if (alive.current && contextRef.current === context) void refreshRepo(repo); }, delay);
          }
        }
        if (finished) break;
        await sleep(1000);
        job = await request<Job>(`/api/bots/jobs?${new URLSearchParams({ provider: providerId, id: job.id })}`);
      }
      const failed = job.items.filter((item) => item.status === "failed");
      const succeeded = job.items.filter((item) => item.status === "done").length;
      if (alive.current) setNotice(`${action}: ${succeeded} succeeded${failed.length ? `, ${failed.length} failed — ${failed.map((item) => `${item.repo}#${item.number}: ${item.error ?? "failed"}`).join("; ")}` : ""}`);
    } catch (reason) {
      if (alive.current) setError(reason instanceof Error ? reason.message : "Lost track of the running action; refresh to see the result.");
      setStatuses(fresh.map((item) => [item.id, null]));
    } finally {
      if (alive.current) setActiveJobs((current) => current.filter((id) => id !== job.id));
    }
  };
  const cancelJobs = () => {
    for (const id of activeJobs) void request("/api/bots/jobs/cancel", { method: "POST", body: JSON.stringify({ provider: providerId, id }) }, csrfToken).catch(() => undefined);
  };

  const skeleton = (
    <div className="bots-skeleton" role="status" aria-live="polite">
            <p><span className="spinner" /> {paging ? "Loading repositories…" : `Loading ${org.trim() || "pull requests"}… listing repositories first, then pull requests.`}</p>
            {[0, 1, 2].map((row) => (
              <div className="skeleton-group" key={row}><i className="skeleton-title" /><i className="skeleton-row" /><i className="skeleton-row" /></div>
            ))}
          </div>
  );

  if (available.length === 0) return <main className="center-state">No connected provider supports bot pull requests.</main>;
  const mention = `@${author.replace(/\[bot\]$/, "")}`;
  const rebase = author.trim() ? `${mention} rebase` : "";
  const disabled = readOnly;

  return (
    <div className="app-shell">
      <header className="topbar">
        <a className="brand" href="/" aria-label="PR Inbox home">
          <img className="brand-mark" src="/logo.svg" alt="" width="30" height="30" />
          <span className="brand-name"><span className="brand-pr">PR</span> Inbox · Bots</span>
        </a>
        <div className="account">
          {readOnly && <span className="mode-badge">Read-only</span>}
        </div>
      </header>
      <main className="content">
        {available.length > 1 && (
          <nav className="providers" aria-label="Providers">
            {available.map((item) => (
              <button key={item.id} type="button" className={item.id === providerId ? "provider active" : "provider"} onClick={() => setProviderId(item.id)} title={item.host}>
                {item.name}
              </button>
            ))}
          </nav>
        )}
        <div className="page-heading"><div><p className="eyebrow">AUTOMATION PULL REQUESTS</p><h1>Bot PRs by repository</h1></div></div>
        <form className="bots-form" onSubmit={(event) => { event.preventDefault(); void load(org, author, true); }}>
          <label>Organization
            <select
              value={customOrg ? otherOrg : org}
              onChange={(event) => {
                if (event.target.value === otherOrg) { setCustomOrg(true); return; }
                setCustomOrg(false);
                setOrg(event.target.value);
                void load(event.target.value, author);
              }}
            >
              {orgs.map((name) => <option key={name} value={name}>{name}</option>)}
              {org && !customOrg && !orgs.includes(org) && <option value={org}>{org}</option>}
              <option value={otherOrg}>Other…</option>
            </select>
          </label>
          {customOrg && (
            <label>Other organization
              <input value={org} onChange={(event) => setOrg(event.target.value)} placeholder="my-org" required autoFocus />
            </label>
          )}
          <label>Author
            <input list="bots-authors" value={author} onChange={(event) => setAuthor(event.target.value)} placeholder="Any author" required={!label.trim()} />
            <datalist id="bots-authors">{authorPresets.map((name) => <option key={name} value={name} />)}</datalist>
          </label>
          <label>Label
            <input list="bots-labels" value={label} onChange={(event) => setLabel(event.target.value)} placeholder="Any label" required={!author.trim()} />
            <datalist id="bots-labels"><option value="renovate" /><option value="dependencies" /></datalist>
          </label>
          <label>Status (this page)
            <select value={status} onChange={(event) => setStatus(event.target.value)}>
              {statusFilters.map(([value, label]) => <option key={value} value={value}>{label}</option>)}
            </select>
          </label>
          <label>Search<input value={query} onChange={(event) => setQuery(event.target.value)} placeholder="repository name" /></label>
          <button className="refresh-button" type="submit" disabled={loading}>{loading ? "Refreshing…" : "Refresh"}</button>
          {listing && <span className="bots-updated">Updated {new Date(listing.fetchedAt).toLocaleTimeString()}</span>}
        </form>
        <p className="bots-notice">Clear Author to match a label from any account. Set both to match author and label.</p>

        {error && <p className="error-notice" role="alert">{error}</p>}
        {notice && <p className="bots-notice" role="status">{notice}</p>}

        {loading && !listing && skeleton}
        {listing && (
          <>
            <div className="bots-bar">
              <label><input type="checkbox" checked={visible.length > 0 && targets.length === visible.length} onChange={(event) => toggle(visible.map((item) => item.id), event.target.checked)} /> Select all ({visible.length})</label>
              {activeJobs.length > 0 && <><span className="bots-working">Working on {[...rowStatus.values()].filter((value) => value.status === "queued" || value.status === "running").length} item(s)…</span><button type="button" onClick={cancelJobs}>Cancel queued</button></>}
              <span>{targets.length} selected{listing.incomplete ? ` · showing ${listing.items.length} of ${listing.totalCount}` : ""}</span>
              <button type="button" disabled={disabled || targets.length === 0} onClick={() => void run("approve", targets.some((item) => item.review !== "approved") ? targets.filter((item) => item.review !== "approved") : targets)}>{targets.length > 0 && targets.every((item) => item.review === "approved") ? "Approve again" : "Approve"}</button>
              <select aria-label="Merge method" value={method} onChange={(event) => setMethod(event.target.value)}>
                {(listing.mergeMethods ?? []).map((value) => <option key={value} value={value}>{value}</option>)}
              </select>
              {confirmMerge ? (
                <>
                  <button type="button" className="danger" disabled={disabled} onClick={() => void run("merge", targets)}>Confirm merge of {targets.length}</button>
                  <button type="button" onClick={() => setConfirmMerge(false)}>Cancel</button>
                </>
              ) : (
                <button type="button" disabled={disabled || targets.length === 0} onClick={() => { setConfirmClose(false); setConfirmMerge(true); }}>Merge…</button>
              )}
              {confirmClose ? (
                <>
                  <button type="button" className="danger" disabled={disabled} onClick={() => void run("close", targets)}>Confirm close of {targets.length}</button>
                  <button type="button" onClick={() => setConfirmClose(false)}>Cancel</button>
                </>
              ) : (
                <button type="button" disabled={disabled || targets.length === 0} onClick={() => { setConfirmMerge(false); setConfirmClose(true); }}>Close PR…</button>
              )}
              <input aria-label="Comment" value={comment} onChange={(event) => setComment(event.target.value)} placeholder={rebase || "Comment on selected PRs"} />
              <button type="button" disabled={disabled || targets.length === 0 || !(comment.trim() || rebase)} onClick={() => void run("comment", targets, comment.trim() || rebase)}>Comment</button>
            </div>
            {index && (
              <div className="bots-pager">
                <span>{index.total} PR(s) in {index.repos.filter((item) => item.count > 0).length} repo(s){index.incomplete ? " (first results only)" : ""}</span>
                <span className="bots-working bots-pager-loading" aria-hidden={!loading} style={{ visibility: loading ? "visible" : "hidden" }}><span className="spinner" /> Loading…</span>
                <label className="bots-pagesize">Repos per page
                  <select value={pageSize} disabled={loading} onChange={(event) => changePageSize(Number(event.target.value))}>
                    {pageSizeOptions.map((value) => <option key={value} value={value}>{value}</option>)}
                  </select>
                </label>
                <details className="bots-repopick">
                  <summary>{pickedRepos.size === 0 ? "All repos" : `${pickedRepos.size} repo(s) selected`}</summary>
                  <div className="bots-repopick-menu">
                    <button type="button" disabled={loading || pickedRepos.size === 0} onClick={() => changePicked(new Set())}>Show all</button>
                    {index.repos.map((item) => (
                      <label key={item.repo}>
                        <input type="checkbox" checked={pickedRepos.has(item.repo)} disabled={loading} onChange={(event) => { const next = new Set(pickedRepos); if (event.target.checked) next.add(item.repo); else next.delete(item.repo); changePicked(next); }} />
                        {item.repo} ({item.count})
                      </label>
                    ))}
                  </div>
                </details>
                {matching.length > pageSize && (
                  <>
                    <button type="button" disabled={loading || page === 0} onClick={() => void goToPage(0)}>« First</button>
                    <button type="button" disabled={loading || page === 0} onClick={() => void goToPage(page - 1)}>‹ Prev</button>
                    <span>Repos {page * pageSize + 1}–{Math.min(matching.length, (page + 1) * pageSize)} of {matching.length}</span>
                    <button type="button" disabled={loading || (page + 1) * pageSize >= matching.length} onClick={() => void goToPage(page + 1)}>Next ›</button>
                    <button type="button" disabled={loading || (page + 1) * pageSize >= matching.length} onClick={() => void goToPage(Math.ceil(matching.length / pageSize) - 1)}>Last »</button>
                  </>
                )}
              </div>
            )}
            {paging ? skeleton : <>
            {groups.length === 0 && <p className="subheading">No matching pull requests.</p>}
            {groups.length > 1 && (
              <div className="bots-collapse-all">
                <button type="button" onClick={() => setExpanded(allCollapsed ? new Set(groups.map(([repo]) => repo)) : new Set())}>{allCollapsed ? "Expand all" : "Collapse all"}</button>
              </div>
            )}
            {groups.map(([repo, items]) => (
              <section className="bots-group" key={repo}>
                <h3>
                  <button type="button" className="bots-chevron" aria-expanded={expanded.has(repo)} aria-label={`${expanded.has(repo) ? "Collapse" : "Expand"} ${repo}`} onClick={() => toggleExpanded(repo)}>{expanded.has(repo) ? "▾" : "▸"}</button>
                  <label><input type="checkbox" aria-label={`Select ${repo}`} checked={items.every((item) => selected.has(item.id))} onChange={(event) => toggle(items.map((item) => item.id), event.target.checked)} /> </label>
                  <a className="bots-repo-link" href={repoURL(items[0].html_url)} target="_blank" rel="noreferrer" title={`Open ${repo}`}>{repo}</a>
                  <button type="button" className="bots-chevron" title={`Refresh ${repo}`} aria-label={`Refresh ${repo}`} disabled={refreshing.has(repo)} onClick={() => void refreshRepo(repo)}>{refreshing.has(repo) ? "…" : "↻"}</button>
                  <span className="pr-number">{items.length}{items.some((item) => selected.has(item.id)) ? ` · ${items.filter((item) => selected.has(item.id)).length} selected` : ""}</span>
                </h3>
                {expanded.has(repo) && <ul className="pr-list">
                  {items.map((item) => (
                    <li className="pr-card bots-row" key={item.id}>
                      <input type="checkbox" aria-label={`Select ${repo}#${item.number}`} checked={selected.has(item.id)} onChange={(event) => toggle([item.id], event.target.checked)} />
                      <div className="pr-main">
                        <div className="repo-line"><a className="pr-title" href={item.html_url} target="_blank" rel="noreferrer">{item.title}</a><span className="pr-number">#{item.number}</span></div>
                        <p className="pr-meta">{item.branch} → {item.baseBranch} · {new Date(item.created_at).toLocaleDateString()}</p>
                      </div>
                      <div className="pr-details">
                        <span className={`ci-label ci-${item.ci}`}>{ciText[item.ci] ?? item.ci}</span>
                        <span className={`merge-label merge-${item.mergeable}`}>{mergeText[item.mergeable] ?? item.mergeable}</span>
                        <span className={`reason-label${item.review === "approved" ? " review-approved" : ""}`}>{item.review === "approved" ? "✓ " : ""}{reviewText[item.review] ?? item.review}</span>
                        {rowStatus.get(item.id) && <span className={`job-label job-${rowStatus.get(item.id)?.status}`} title={rowStatus.get(item.id)?.error}>{rowStatusText[rowStatus.get(item.id)?.status ?? ""] ?? rowStatus.get(item.id)?.status}</span>}
                        {(
                          customFor === item.id ? (
                            <form className="bots-custom" onSubmit={(event) => { event.preventDefault(); if (customText.trim()) { void run("comment", [item], customText.trim()); setCustomFor(null); } }}>
                              <input aria-label="Custom command" value={customText} onChange={(event) => setCustomText(event.target.value)} placeholder={`@${item.author.replace(/\[bot\]$/, "")} …`} autoFocus />
                              <button type="submit" disabled={disabled || !customText.trim()}>Send</button>
                              <button type="button" onClick={() => setCustomFor(null)}>Cancel</button>
                            </form>
                          ) : (
                            <select
                              aria-label={`Ask ${item.author} to…`}
                              value=""
                              disabled={disabled || isActive(item.id)}
                              onChange={(event) => {
                                const value = event.target.value;
                                if (value === "custom") { setCustomText(`@${item.author.replace(/\[bot\]$/, "")} `); setCustomFor(item.id); }
                                else if (value) void run("comment", [item], `@${item.author.replace(/\[bot\]$/, "")} ${value}`);
                              }}
                            >
                              <option value="">Ask {item.author} to…</option>
                              <option value="rebase">Rebase</option>
                              <option value="recreate">Recreate</option>
                              <option value="custom">Custom command…</option>
                            </select>
                          )
                        )}
                      </div>
                    </li>
                  ))}
                </ul>}
              </section>
            ))}
          </>}
          </>
        )}
      </main>
    </div>
  );
}
