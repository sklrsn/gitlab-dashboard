import { useCallback, useEffect, useRef, useState } from "react";
import { IconAlertTriangle, IconExternalLink, IconRefresh } from "@tabler/icons-react";
import { api, type AppConfig, type Pipeline, type Scope } from "./api";
import { ScopePicker } from "./ScopePicker";
import { PipelineRow, type ChildState } from "./PipelineRow";
import { isActive, statusFilterOptions } from "./status";
import { timeAgo } from "./format";

type View = { scope?: Scope; status: string; ref: string };

const PAGE = 20;
const FAST_POLL = 10_000;
const SLOW_POLL = 30_000;

function readView(def?: Scope): View {
  const q = new URLSearchParams(location.search);
  const group = q.get("group");
  const project = q.get("project");
  const scope: Scope | undefined = project ? { kind: "project", path: project } : group ? { kind: "group", path: group } : def;
  return { scope, status: q.get("status") ?? "", ref: q.get("ref") ?? "" };
}

function writeView(v: View, push: boolean) {
  const q = new URLSearchParams();
  if (v.scope) q.set(v.scope.kind, v.scope.path);
  if (v.status) q.set("status", v.status);
  if (v.ref) q.set("ref", v.ref);
  const url = q.size ? `?${q}` : location.pathname;
  if (push) history.pushState(null, "", url);
  else history.replaceState(null, "", url);
}

function mergeRows(fresh: Pipeline[], old: Pipeline[]): Pipeline[] {
  const seen = new Set(fresh.map((p) => p.key));
  return [...fresh, ...old.filter((p) => !seen.has(p.key))].sort((a, b) => (b.id ?? 0) - (a.id ?? 0));
}

function anyActive(rows: Pipeline[], kids: Record<string, ChildState>) {
  return rows.some((p) => isActive(p.status)) || Object.values(kids).some((k) => k.items?.some((p) => isActive(p.status)));
}

export function App() {
  const [config, setConfig] = useState<AppConfig>();
  const [configErr, setConfigErr] = useState("");
  const [view, setView] = useState<View>({ status: "", ref: "" });
  const [refDraft, setRefDraft] = useState("");
  const [rows, setRows] = useState<Pipeline[]>([]);
  const [cursor, setCursor] = useState<string>();
  const [hasMore, setHasMore] = useState(false);
  const [limit, setLimit] = useState(PAGE);
  const [pages, setPages] = useState(1);
  const [warnings, setWarnings] = useState<string[]>([]);
  const [error, setError] = useState("");
  const [loading, setLoading] = useState(false);
  const [updatedAt, setUpdatedAt] = useState<number>();
  const [expanded, setExpanded] = useState<Set<string>>(new Set());
  const [children, setChildren] = useState<Record<string, ChildState>>({});
  const [now, setNow] = useState(Date.now());
  const gen = useRef(0);
  const expandedPipelines = useRef(new Map<string, Pipeline>());

  useEffect(() => {
    api
      .config()
      .then((c) => {
        setConfig(c);
        document.title = c.title;
        const v = readView(c.defaultScope);
        setView(v);
        setRefDraft(v.ref);
        writeView(v, false);
      })
      .catch((e) => setConfigErr(e.message));
    const onPop = () => setView((cur) => {
      const v = readView(cur.scope);
      setRefDraft(v.ref);
      return v;
    });
    window.addEventListener("popstate", onPop);
    return () => window.removeEventListener("popstate", onPop);
  }, []);

  useEffect(() => {
    const t = setInterval(() => setNow(Date.now()), 5_000);
    return () => clearInterval(t);
  }, []);

  const loadChildren = useCallback(async (p: Pipeline, g: number) => {
    setChildren((c) => ({ ...c, [p.key]: { ...c[p.key], loading: true } }));
    try {
      const items = await api.children(p);
      if (g !== gen.current) return;
      setChildren((c) => ({ ...c, [p.key]: { loading: false, items } }));
    } catch (e) {
      if (g !== gen.current) return;
      setChildren((c) => ({ ...c, [p.key]: { ...c[p.key], loading: false, error: (e as Error).message } }));
    }
  }, []);

  // Fresh load whenever the scope or filters change.
  useEffect(() => {
    const g = ++gen.current;
    setRows([]);
    setCursor(undefined);
    setHasMore(false);
    setLimit(PAGE);
    setPages(1);
    setWarnings([]);
    setError("");
    setExpanded(new Set());
    setChildren({});
    expandedPipelines.current.clear();
    if (!view.scope) return;
    setLoading(true);
    api
      .pipelines(view.scope, { status: view.status, ref: view.ref, limit: PAGE })
      .then((pg) => {
        if (g !== gen.current) return;
        setRows(pg.pipelines);
        setCursor(pg.nextCursor);
        setHasMore(pg.hasMore);
        setWarnings(pg.warnings ?? []);
        setUpdatedAt(Date.now());
      })
      .catch((e) => g === gen.current && setError(e.message))
      .finally(() => g === gen.current && setLoading(false));
  }, [view]);

  const refresh = useCallback(async () => {
    const scope = view.scope;
    if (!scope) return;
    const g = gen.current;
    try {
      const pg = await api.pipelines(scope, { status: view.status, ref: view.ref, limit });
      if (g !== gen.current) return;
      if (scope.kind === "group" || pages === 1) {
        setRows(pg.pipelines);
        setCursor(pg.nextCursor);
        setHasMore(pg.hasMore);
      } else {
        setRows((old) => mergeRows(pg.pipelines, old));
      }
      setWarnings(pg.warnings ?? []);
      setError("");
      setUpdatedAt(Date.now());
      await Promise.all([...expandedPipelines.current.values()].map((p) => loadChildren(p, g)));
    } catch (e) {
      if (g === gen.current) setError((e as Error).message);
    }
  }, [view, limit, pages, loadChildren]);

  // Poll quickly while something is running, slowly otherwise; pause in background tabs.
  useEffect(() => {
    if (!view.scope) return;
    const delay = anyActive(rows, children) ? FAST_POLL : SLOW_POLL;
    let t = setTimeout(function tick() {
      if (document.hidden) t = setTimeout(tick, delay);
      else refresh();
    }, delay);
    const onVis = () => {
      if (!document.hidden && updatedAt && Date.now() - updatedAt > delay) refresh();
    };
    document.addEventListener("visibilitychange", onVis);
    return () => {
      clearTimeout(t);
      document.removeEventListener("visibilitychange", onVis);
    };
  }, [view.scope, rows, children, refresh, updatedAt]);

  const loadMore = async () => {
    const scope = view.scope;
    if (!scope) return;
    const g = gen.current;
    setLoading(true);
    try {
      if (scope.kind === "project") {
        const pg = await api.pipelines(scope, { status: view.status, ref: view.ref, cursor });
        if (g !== gen.current) return;
        setRows((old) => mergeRows(pg.pipelines, old));
        setCursor(pg.nextCursor);
        setHasMore(pg.hasMore);
        setPages((n) => n + 1);
      } else {
        const next = limit + PAGE;
        const pg = await api.pipelines(scope, { status: view.status, ref: view.ref, limit: next });
        if (g !== gen.current) return;
        setRows(pg.pipelines);
        setHasMore(pg.hasMore);
        setLimit(next);
      }
      setError("");
    } catch (e) {
      if (g === gen.current) setError((e as Error).message);
    } finally {
      if (g === gen.current) setLoading(false);
    }
  };

  const toggle = (p: Pipeline) => {
    const next = new Set(expanded);
    if (next.has(p.key)) {
      next.delete(p.key);
      expandedPipelines.current.delete(p.key);
    } else {
      next.add(p.key);
      expandedPipelines.current.set(p.key, p);
      loadChildren(p, gen.current);
    }
    setExpanded(next);
  };

  const update = (patch: Partial<View>, push = false) => {
    const v = { ...view, ...patch };
    writeView(v, push);
    setView(v);
  };

  const scopeUrl =
    config && view.scope
      ? view.scope.kind === "project"
        ? `${config.gitlabUrl}/${view.scope.path}/-/pipelines`
        : `${config.gitlabUrl}/${view.scope.path}`
      : undefined;

  return (
    <div className="app">
      <header className="topbar">
        <div className="brand">
          {config?.hasLogo && <img src="/logo" alt="" className="logo" />}
          <span className="app-title">{config?.title ?? ""}</span>
        </div>
        <div className="topbar-right">
          <ScopePicker value={view.scope} onChange={(s) => update({ scope: s }, true)} />
          {scopeUrl && (
            <a className="btn-primary" href={scopeUrl} target="_blank" rel="noopener noreferrer">
              <IconExternalLink size={15} aria-hidden /> Open in GitLab
            </a>
          )}
        </div>
      </header>

      {view.scope && (
        <div className="filters">
          <select value={view.status} onChange={(e) => update({ status: e.target.value })} aria-label="Status">
            {statusFilterOptions.map((o) => (
              <option key={o.value} value={o.value}>
                {o.label}
              </option>
            ))}
          </select>
          <input
            className="ref-input"
            value={refDraft}
            onChange={(e) => setRefDraft(e.target.value)}
            onKeyDown={(e) => e.key === "Enter" && update({ ref: refDraft.trim() })}
            onBlur={() => refDraft.trim() !== view.ref && update({ ref: refDraft.trim() })}
            placeholder="Branch or tag, e.g. main"
            aria-label="Branch or tag"
          />
          <span className="spacer" />
          {updatedAt && <span className="dim small">updated {timeAgo(new Date(updatedAt).toISOString(), now)}</span>}
          <button className="icon-btn" onClick={refresh} aria-label="Refresh now" title="Refresh now">
            <IconRefresh size={16} />
          </button>
        </div>
      )}

      <main>
        {configErr && <div className="banner error">{configErr}</div>}
        {error && (
          <div className="banner error">
            <IconAlertTriangle size={16} aria-hidden /> {error}
          </div>
        )}
        {warnings.map((w) => (
          <div key={w} className="banner warn">
            {w}
          </div>
        ))}

        {!view.scope && config && (
          <div className="empty">
            <h2>Pick a group or project</h2>
            <p>Use the selector at the top right to choose what to watch. Your choice stays in the address bar, so you can bookmark or share it.</p>
          </div>
        )}

        {view.scope && (
          <div className="list" role="list">
            {rows.map((p) => (
              <PipelineRow
                key={p.key}
                p={p}
                depth={0}
                showProject={view.scope?.kind === "group"}
                expanded={expanded}
                children={children}
                onToggle={toggle}
                now={now}
              />
            ))}
            {!loading && !error && rows.length === 0 && <div className="row note">No pipelines match these filters.</div>}
            {loading && <div className="row note">Loading pipelines…</div>}
          </div>
        )}

        {hasMore && !loading && (
          <div className="more">
            <button className="btn-secondary" onClick={loadMore}>
              Load more
            </button>
          </div>
        )}
      </main>
    </div>
  );
}
