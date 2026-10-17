import { useCallback, useEffect, useRef, useState } from "react";
import { request } from "./api";

export type SnoozeTarget = { id: number; number: number; repo: string; title: string; html_url: string; author: string; created_at: string; updated_at: string; draft: boolean | null; ci: "success" | "failure" | "pending" | "none" | "unknown" };
type SavedSnooze = { target: SnoozeTarget; until: string; createdAt: string };
type Snapshot = { items: SavedSnooze[]; now: string; receivedAt: number };
type Payload = Omit<Snapshot, "receivedAt">;

export function useSnoozes(provider: string, csrfToken: string) {
  const [snapshots, setSnapshots] = useState<Record<string, Snapshot>>({});
  const [errors, setErrors] = useState<Record<string, string>>({});
  const [busy, setBusy] = useState<Record<string, boolean>>({});
  const [clock, setClock] = useState(Date.now());
  const versions = useRef<Record<string, number>>({});
  const mutations = useRef<Record<string, boolean>>({});
  const saveSnapshot = useCallback((id: string, data: Payload) => {
    setSnapshots((current) => ({ ...current, [id]: { ...data, receivedAt: Date.now() } }));
    setErrors((current) => ({ ...current, [id]: "" }));
    setClock(Date.now());
  }, []);
  const load = useCallback(async () => {
    if (!provider || mutations.current[provider]) return;
    const version = versions.current[provider] = (versions.current[provider] ?? 0) + 1;
    try {
      const data = await request<Payload>(`/api/snooze?provider=${encodeURIComponent(provider)}`);
      if (versions.current[provider] === version) saveSnapshot(provider, data);
    } catch (reason) { if (versions.current[provider] === version) setErrors((current) => ({ ...current, [provider]: reason instanceof Error ? reason.message : "Could not load snoozes." })); }
  }, [provider, saveSnapshot]);
  useEffect(() => {
    void load();
    const poll = window.setInterval(() => void load(), 30_000);
    const tick = window.setInterval(() => setClock(Date.now()), 1000);
    const resume = () => { setClock(Date.now()); void load(); };
    const visible = () => { if (document.visibilityState === "visible") resume(); };
    window.addEventListener("focus", resume);
    document.addEventListener("visibilitychange", visible);
    return () => { clearInterval(poll); clearInterval(tick); window.removeEventListener("focus", resume); document.removeEventListener("visibilitychange", visible); };
  }, [load]);
  const update = async (id: number, until?: string) => {
    if (mutations.current[provider]) return;
    mutations.current[provider] = true;
    versions.current[provider] = (versions.current[provider] ?? 0) + 1;
    setBusy((current) => ({ ...current, [provider]: true }));
    try {
      saveSnapshot(provider, await request<Payload>(`/api/snooze?provider=${encodeURIComponent(provider)}`, { method: "POST", body: JSON.stringify({ id, ...(until ? { until } : { remove: true }) }) }, csrfToken));
    } catch (reason) { setErrors((current) => ({ ...current, [provider]: reason instanceof Error ? reason.message : "Could not save snooze." })); }
    finally { mutations.current[provider] = false; setBusy((current) => ({ ...current, [provider]: false })); }
  };
  const snapshot = snapshots[provider];
  const now = clock + (snapshot ? Date.parse(snapshot.now) - snapshot.receivedAt : 0);
  const active = snapshot?.items.filter((item) => Date.parse(item.until) > now) ?? [];
  const expired = snapshot?.items.filter((item) => Date.parse(item.until) <= now) ?? [];
  return { active, expired, hidden: new Set(active.map((item) => item.target.id)), error: errors[provider] ?? "", busy: !!busy[provider], loaded: !!snapshot, now, update, reload: load };
}
export type SnoozeState = ReturnType<typeof useSnoozes>;

export function SnoozeControl({ id, state, readOnly }: { id: number; state: SnoozeState; readOnly: boolean }) {
  const [duration, setDuration] = useState("4");
  const [custom, setCustom] = useState("");
  const [error, setError] = useState("");
  return <form className="snooze-control" onSubmit={(event) => {
    event.preventDefault();
    const deadline = duration === "custom" ? new Date(custom).getTime() : state.now + Number(duration) * 3600_000;
    if (!Number.isFinite(deadline) || deadline <= state.now) { setError("Choose a future date and time."); return; }
    setError(""); void state.update(id, new Date(deadline).toISOString());
  }}>
    <label> Snooze for <select aria-label="Snooze duration" value={duration} onChange={(event) => setDuration(event.target.value)} disabled={readOnly || state.busy || !state.loaded}>
      <option value="1">1 hour</option><option value="4">4 hours</option><option value="24">24 hours</option><option value="custom">Until…</option>
    </select></label>
    {duration === "custom" && <input aria-label="Snooze until" type="datetime-local" required value={custom} onChange={(event) => setCustom(event.target.value)} disabled={readOnly || state.busy || !state.loaded} />}
    <button type="submit" disabled={readOnly || state.busy || !state.loaded}>Snooze</button>
    {error && <span role="alert">{error}</span>}
  </form>;
}

export function SnoozePanel({ state, readOnly, onSelect }: { state: SnoozeState; readOnly: boolean; onSelect: (target: SnoozeTarget) => void }) {
  const row = (item: SavedSnooze, expired: boolean) => <li className="snooze-row" key={item.target.id}>
    <div><button type="button" className="snooze-title" onClick={() => onSelect(item.target)}>{item.target.repo} #{item.target.number} · {item.target.title}</button>
      <p>{expired ? "Snooze ended" : "Snoozed until"} {new Date(item.until).toLocaleString()}</p></div>
    <div className="snooze-row-actions"><button type="button" disabled={readOnly || state.busy} onClick={() => void state.update(item.target.id)}>{expired ? "Dismiss reminder" : "Cancel snooze"}</button>
      <SnoozeControl id={item.target.id} state={state} readOnly={readOnly} /></div>
  </li>;
  return <section className="snooze-panel" aria-label="Snoozes">
    {state.error && <div className="notice" role="alert">{state.error} {state.loaded ? "Showing the last loaded snoozes." : "Items remain visible until snoozes can be loaded."} <button type="button" onClick={() => void state.reload()}>Retry snoozes</button></div>}
    {state.expired.length > 0 && <div className="snooze-overdue" role="status"><h2>Snooze ended ({state.expired.length})</h2><p>These items are no longer hidden. Reminders remain here until dismissed or snoozed again, including after restarting the app.</p><ul>{state.expired.map((item) => row(item, true))}</ul></div>}
    {state.active.length > 0 && <p>{state.active.length} snoozed {state.active.length === 1 ? "item is" : "items are"} hidden from the queues. Queue totals include snoozed items.</p>}
    <details><summary>Snoozed ({state.active.length})</summary><p>Snoozes continue while the app is closed. Saved titles may be out of date; open an item to load fresh details.</p>
      {state.active.length ? <ul>{state.active.map((item) => row(item, false))}</ul> : <p>No active snoozes.</p>}
    </details>
  </section>;
}
