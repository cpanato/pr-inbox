import React, { useCallback, useEffect, useMemo, useState } from "react";
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
  const [listing, setListing] = useState<Listing | null>(null);
  const [loading, setLoading] = useState(false);
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState("");
  const [notice, setNotice] = useState("");
  const [selected, setSelected] = useState<Set<number>>(new Set());
  const [status, setStatus] = useState<string>("all");
  const [query, setQuery] = useState("");
  const [method, setMethod] = useState("");
  const [comment, setComment] = useState("");
  const [confirmMerge, setConfirmMerge] = useState(false);
  const [customFor, setCustomFor] = useState<number | null>(null);
  const [customText, setCustomText] = useState("");

  const load = useCallback(async (nextOrg: string, nextAuthor: string, keep = false) => {
    if (!nextOrg.trim() || !nextAuthor.trim()) return;
    setLoading(true);
    setError("");
    try {
      const params = new URLSearchParams({ provider: providerId, org: nextOrg.trim(), author: nextAuthor.trim() });
      const data = await request<Listing>(`/api/bots?${params}`);
      setListing(data);
      setMethod((current) => (data.mergeMethods.includes(current) ? current : data.mergeMethods[0] ?? ""));
      setSelected((current) => {
        const present = new Set(data.items.map((item) => item.id));
        return keep ? new Set([...current].filter((id) => present.has(id))) : new Set();
      });
      storageSet("bots.org", nextOrg.trim());
      storageSet("bots.author", nextAuthor.trim());
    } catch (reason) {
      setError(reason instanceof Error ? reason.message : "Could not load pull requests.");
    } finally {
      setLoading(false);
    }
  }, [providerId]);

  useEffect(() => {
    if (!providerId) return;
    setListing(null);
    setOrgs([]);
    void request<{ orgs: string[] }>(`/api/bots/orgs?provider=${encodeURIComponent(providerId)}`)
      .then((data) => {
        setOrgs(data.orgs);
        const initial = org || data.orgs[0] || "";
        if (initial) { setOrg(initial); void load(initial, author); }
      })
      .catch(() => { if (org) void load(org, author); });
    // Only reload when the provider changes.
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [providerId]);

  const visible = useMemo(() => (listing?.items ?? []).filter((item) => {
    if (status === "ready" && !(item.mergeable === "clean" && item.ci === "success")) return false;
    if (status === "conflicts" && item.mergeable !== "conflicting") return false;
    if (status === "failing" && item.ci !== "failure") return false;
    if (status === "pending" && item.ci !== "pending") return false;
    return `${item.repo} ${item.title}`.toLowerCase().includes(query.toLowerCase());
  }), [listing, status, query]);
  const groups = useMemo(() => {
    const map = new Map<string, BotPR[]>();
    for (const item of visible) map.set(item.repo, [...(map.get(item.repo) ?? []), item]);
    return [...map.entries()];
  }, [visible]);
  const targets = visible.filter((item) => selected.has(item.id));

  const toggle = (ids: number[], on: boolean) => setSelected((current) => {
    const next = new Set(current);
    for (const id of ids) on ? next.add(id) : next.delete(id);
    return next;
  });

  const run = async (action: "approve" | "merge" | "comment", items: BotPR[], body = "") => {
    if (items.length === 0 || busy) return;
    setBusy(true);
    setNotice("");
    const failures: string[] = [];
    for (const item of items) {
      try {
        await request("/api/bots/action", { method: "POST", body: JSON.stringify({ provider: providerId, id: item.id, action, method, body }) }, csrfToken);
      } catch (reason) {
        failures.push(`${item.repo}#${item.number}: ${reason instanceof Error ? reason.message : "failed"}`);
      }
    }
    setNotice(`${action}: ${items.length - failures.length} succeeded${failures.length ? `, ${failures.length} failed — ${failures.join("; ")}` : ""}`);
    setConfirmMerge(false);
    setBusy(false);
    await load(org, author, true);
  };

  if (available.length === 0) return <main className="center-state">No connected provider supports bot pull requests.</main>;
  const mention = `@${author.replace(/\[bot\]$/, "")}`;
  const rebase = `${mention} rebase`;
  const disabled = readOnly || busy || loading;

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
            <input list="bots-authors" value={author} onChange={(event) => setAuthor(event.target.value)} placeholder="dependabot" required />
            <datalist id="bots-authors">{authorPresets.map((name) => <option key={name} value={name} />)}</datalist>
          </label>
          <label>Status
            <select value={status} onChange={(event) => setStatus(event.target.value)}>
              {statusFilters.map(([value, label]) => <option key={value} value={value}>{label}</option>)}
            </select>
          </label>
          <label>Search<input value={query} onChange={(event) => setQuery(event.target.value)} placeholder="repo or title" /></label>
          <button className="refresh-button" type="submit" disabled={loading}>{loading ? "Refreshing…" : "Refresh"}</button>
          {listing && <span className="bots-updated">Updated {new Date(listing.fetchedAt).toLocaleTimeString()}</span>}
        </form>

        {error && <p className="error-notice" role="alert">{error}</p>}
        {notice && <p className="bots-notice" role="status">{notice}</p>}

        {listing && (
          <>
            <div className="bots-bar">
              <label><input type="checkbox" checked={visible.length > 0 && targets.length === visible.length} onChange={(event) => toggle(visible.map((item) => item.id), event.target.checked)} /> Select all ({visible.length})</label>
              <span>{targets.length} selected{listing.incomplete ? ` · showing ${listing.items.length} of ${listing.totalCount}` : ""}</span>
              <button type="button" disabled={disabled || targets.length === 0} onClick={() => void run("approve", targets)}>Approve</button>
              <select aria-label="Merge method" value={method} onChange={(event) => setMethod(event.target.value)}>
                {listing.mergeMethods.map((value) => <option key={value} value={value}>{value}</option>)}
              </select>
              {confirmMerge ? (
                <>
                  <button type="button" className="danger" disabled={disabled} onClick={() => void run("merge", targets)}>Confirm merge of {targets.length}</button>
                  <button type="button" onClick={() => setConfirmMerge(false)}>Cancel</button>
                </>
              ) : (
                <button type="button" disabled={disabled || targets.length === 0} onClick={() => setConfirmMerge(true)}>Merge…</button>
              )}
              <input aria-label="Comment" value={comment} onChange={(event) => setComment(event.target.value)} placeholder={rebase} />
              <button type="button" disabled={disabled || targets.length === 0} onClick={() => void run("comment", targets, comment.trim() || rebase)}>Comment</button>
            </div>
            {groups.length === 0 && <p className="subheading">No matching pull requests.</p>}
            {groups.map(([repo, items]) => (
              <section className="bots-group" key={repo}>
                <h3>
                  <label><input type="checkbox" checked={items.every((item) => selected.has(item.id))} onChange={(event) => toggle(items.map((item) => item.id), event.target.checked)} /> {repo}</label>
                  <span className="pr-number">{items.length}</span>
                </h3>
                <ul className="pr-list">
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
                        <span className="reason-label">{reviewText[item.review] ?? item.review}</span>
                        {item.mergeable === "conflicting" && (
                          customFor === item.id ? (
                            <form className="bots-custom" onSubmit={(event) => { event.preventDefault(); if (customText.trim()) { void run("comment", [item], customText.trim()); setCustomFor(null); } }}>
                              <input aria-label="Custom command" value={customText} onChange={(event) => setCustomText(event.target.value)} placeholder={`${mention} …`} autoFocus />
                              <button type="submit" disabled={disabled || !customText.trim()}>Send</button>
                              <button type="button" onClick={() => setCustomFor(null)}>Cancel</button>
                            </form>
                          ) : (
                            <select
                              aria-label={`Ask ${author} to…`}
                              value=""
                              disabled={disabled}
                              onChange={(event) => {
                                const value = event.target.value;
                                if (value === "custom") { setCustomText(`${mention} `); setCustomFor(item.id); }
                                else if (value) void run("comment", [item], `${mention} ${value}`);
                              }}
                            >
                              <option value="">Ask {author} to…</option>
                              <option value="rebase">Rebase</option>
                              <option value="recreate">Recreate</option>
                              <option value="custom">Custom command…</option>
                            </select>
                          )
                        )}
                      </div>
                    </li>
                  ))}
                </ul>
              </section>
            ))}
          </>
        )}
      </main>
    </div>
  );
}
