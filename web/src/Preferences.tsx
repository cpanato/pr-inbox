import { useCallback, useEffect, useRef, useState } from "react";
import { request, type APIError } from "./api";

type Preferences = { tab: "review" | "mine" | "history"; query: string; org: string; repo: string; order: "oldest" | "recent" };
type Saved = { preferences: Preferences; version: string };
type Entry = { values: Preferences; version: string; revision: number; savedRevision: number; loaded: boolean; loading: boolean; saving: boolean; error: string; conflict: boolean; timer?: number };
const defaults = (): Preferences => ({ tab: "review", query: "", org: "", repo: "", order: "oldest" });

export function usePreferences(provider: string, csrfToken: string, readOnly: boolean) {
  const entries = useRef<Record<string, Entry>>({});
  const [, setRender] = useState(0);
  const render = useCallback(() => setRender((value) => value + 1), []);
  const get = useCallback((id: string) => entries.current[id] ??= { values: defaults(), version: "", revision: 0, savedRevision: 0, loaded: false, loading: false, saving: false, error: "", conflict: false }, []);
  const load = useCallback(async (id: string) => {
    if (!id) return;
    const entry = get(id);
    if (entry.loading || entry.saving) return;
    clearTimeout(entry.timer); entry.loading = true; render();
    try {
      const data = await request<Saved>(`/api/preferences?provider=${encodeURIComponent(id)}`);
      Object.assign(entry, { values: data.preferences, version: data.version, revision: 0, savedRevision: 0, loaded: true, error: "", conflict: false });
    } catch (reason) { entry.error = reason instanceof Error ? reason.message : "Could not load preferences."; }
    finally { entry.loading = false; render(); }
  }, [get, render]);
  const persist = useCallback(async function save(id: string) {
    const entry = get(id);
    if (readOnly || !entry.loaded || entry.loading || entry.saving || entry.error || entry.savedRevision === entry.revision) return;
    clearTimeout(entry.timer);
    const revision = entry.revision;
    const body = JSON.stringify({ preferences: entry.values, version: entry.version });
    entry.saving = true; render();
    try {
      const data = await request<Saved>(`/api/preferences?provider=${encodeURIComponent(id)}`, { method: "POST", body }, csrfToken);
      entry.version = data.version; entry.savedRevision = revision;
    } catch (reason) { entry.error = reason instanceof Error ? reason.message : "Could not save preferences."; entry.conflict = (reason as APIError).code === "preferences_conflict"; }
    finally {
      entry.saving = false; render();
      if (!entry.error && entry.savedRevision !== entry.revision) entry.timer = window.setTimeout(() => void save(id), 0);
    }
  }, [csrfToken, readOnly, get, render]);
  useEffect(() => { if (provider && !get(provider).loaded && !get(provider).error) void load(provider); }, [provider, get, load]);
  useEffect(() => {
    const beforeUnload = (event: BeforeUnloadEvent) => {
      if (!readOnly && Object.values(entries.current).some((entry) => entry.loaded && entry.revision !== entry.savedRevision)) { event.preventDefault(); event.returnValue = ""; }
    };
    window.addEventListener("beforeunload", beforeUnload);
    return () => window.removeEventListener("beforeunload", beforeUnload);
  }, [readOnly]);
  const entry = get(provider);
  const change = (patch: Partial<Preferences>) => {
    if (entry.loading || (!entry.loaded && !entry.error)) return;
    entry.values = { ...entry.values, ...patch }; entry.revision++; render();
    clearTimeout(entry.timer); entry.timer = window.setTimeout(() => void persist(provider), 350);
  };
  return {
    values: entry.values, loading: entry.loading || (!entry.loaded && !entry.error), loaded: entry.loaded, saving: entry.saving,
    dirty: entry.revision !== entry.savedRevision, error: entry.error, conflict: entry.conflict, hasSaved: entry.version !== "", change,
    reset: () => change(defaults()), reload: () => void load(provider),
    retry: () => { entry.error = ""; entry.conflict = false; render(); void persist(provider); },
  };
}

export function PreferencesStatus({ settings, readOnly }: { settings: ReturnType<typeof usePreferences>; readOnly: boolean }) {
  return <div className="preferences-status">
    <span role={settings.error ? "alert" : "status"}>{settings.loading ? "Loading saved preferences…" : settings.error ? `${settings.error} Changes have not been saved.` : readOnly ? "Read-only mode: filter changes are temporary." : settings.saving || settings.dirty ? "Saving preferences…" : settings.hasSaved ? "Preferences saved" : "Using default preferences"}</span>
    {settings.error && !settings.conflict && settings.loaded && settings.dirty && !readOnly && <button type="button" disabled={settings.saving || settings.loading} onClick={settings.retry}>Retry save</button>}
    <button type="button" disabled={settings.loading || settings.saving} onClick={settings.reload}>{settings.dirty ? "Discard changes and reload saved preferences" : "Reload saved preferences"}</button>
    <button type="button" disabled={settings.loading} onClick={settings.reset}>Restore defaults</button>
  </div>;
}
