import { useCallback, useEffect, useRef, useState } from "react";
import { IconAlertTriangle, IconChevronDown, IconChevronRight, IconGitBranch, IconHistory } from "@tabler/icons-react";
import { api, ApiError, type GroupedPage, type Pipeline, type PipelineGroup, type Scope } from "./api";
import { useChildTree } from "./childTree";
import { MRChip, PipelineRow } from "./PipelineRow";
import { StatusBadge, toneOf } from "./status";
import { formatDuration, fullTime, timeAgo } from "./format";

const POLL = 30_000;

type Older = { items: Pipeline[]; next?: number; loading: boolean; error?: string };

type Props = {
  scope: Scope;
  by: string;
  window: string;
  status: string;
  gitRef: string;
  now: number;
  refreshKey: number;
  onUpdated: (t: number) => void;
};

export const groupByOptions = [
  { value: "", label: "No grouping" },
  { value: "name", label: "Workflow name" },
  { value: "child", label: "Child pipeline" },
  { value: "ref", label: "Branch / tag" },
  { value: "source", label: "Trigger source" },
  { value: "project", label: "Project", groupOnly: true },
];

export const windowOptions = [
  { value: "100", label: "Last 100 pipelines" },
  { value: "24h", label: "Last 24 hours" },
  { value: "7d", label: "Last 7 days" },
];

export function GroupedView({ scope, by, window, status, gitRef, now, refreshKey, onUpdated }: Props) {
  const [page, setPage] = useState<GroupedPage>();
  const [error, setError] = useState("");
  const [loading, setLoading] = useState(false);
  const [open, setOpen] = useState<Set<string>>(new Set());
  const [older, setOlder] = useState<Record<string, Older>>({});
  const tree = useChildTree();
  const { reset: resetTree, reloadOpen } = tree;
  const gen = useRef(0);
  const openRef = useRef(open);
  openRef.current = open;

  const fetchPage = useCallback(
    async (g: number) => {
      try {
        const pg = await api.groups(scope, { by, window, status, ref: gitRef, open: [...openRef.current] });
        if (g !== gen.current) return;
        setPage(pg);
        setError("");
        onUpdated(Date.now());
      } catch (e) {
        if (g === gen.current) setError((e as Error).message);
      }
    },
    [scope, by, window, status, gitRef, onUpdated],
  );

  // Fresh load when the scope, grouping, window or filters change.
  useEffect(() => {
    const g = ++gen.current;
    setPage(undefined);
    setError("");
    setOpen(new Set());
    openRef.current = new Set();
    setOlder({});
    resetTree();
    setLoading(true);
    fetchPage(g).finally(() => g === gen.current && setLoading(false));
  }, [fetchPage, resetTree]);

  const refresh = useCallback(async () => {
    await Promise.all([fetchPage(gen.current), reloadOpen()]);
  }, [fetchPage, reloadOpen]);

  const firstKey = useRef(refreshKey);
  useEffect(() => {
    if (refreshKey !== firstKey.current) refresh();
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [refreshKey]);

  // Grouped views read a whole window per refresh, so they poll gently.
  useEffect(() => {
    let t = setTimeout(function tick() {
      if (document.hidden) t = setTimeout(tick, POLL);
      else refresh();
    }, POLL);
    return () => clearTimeout(t);
  }, [page, refresh]);

  const toggleGroup = (key: string) => {
    const next = new Set(open);
    if (next.has(key)) {
      next.delete(key);
      // Closing a group forgets its open branches.
      for (const k of next) if (k.startsWith(key + BRANCH_SEP)) next.delete(k);
    } else next.add(key);
    openRef.current = next;
    setOpen(next);
    if (next.has(key)) fetchPage(gen.current);
  };

  const loadOlder = async (grp: PipelineGroup) => {
    if (!grp.olderProject || !grp.olderName) return;
    const g = gen.current;
    const cur = older[grp.key];
    const pageNo = cur?.next ?? 1;
    setOlder((o) => ({ ...o, [grp.key]: { items: cur?.items ?? [], next: cur?.next, loading: true } }));
    try {
      const pg = await api.runs(grp.olderProject, grp.olderName, pageNo);
      if (g !== gen.current) return;
      setOlder((o) => {
        const items = [...(o[grp.key]?.items ?? []), ...pg.pipelines];
        return { ...o, [grp.key]: { items, next: pg.hasMore ? pageNo + 1 : undefined, loading: false } };
      });
    } catch (e) {
      if (g !== gen.current) return;
      setOlder((o) => ({ ...o, [grp.key]: { ...o[grp.key], loading: false, error: (e as ApiError).message } }));
    }
  };

  const groups = page?.groups ?? [];
  const windowLabel = windowOptions.find((w) => w.value === window)?.label.toLowerCase() ?? "last 100 pipelines";

  return (
    <>
      {error && (
        <div className="banner error">
          <IconAlertTriangle size={16} aria-hidden /> {error}
        </div>
      )}
      {page?.warnings?.map((w) => (
        <div key={w} className="banner warn">
          {w}
        </div>
      ))}
      {page?.capped && (
        <div className="banner warn">
          <IconAlertTriangle size={16} aria-hidden />
          This window holds more pipelines than the dashboard reads at once. Groups cover pipelines since {fullTime(page.since)}.
        </div>
      )}
      {page && (
        <div className="window-note">
          {window === "100" ? (
            <>
              Grouped from the last {page.pipelines} pipeline{page.pipelines === 1 ? "" : "s"}
              {page.since && <> (since {timeAgo(page.since, now)})</>}
            </>
          ) : (
            <>
              Grouped from {page.pipelines} pipeline{page.pipelines === 1 ? "" : "s"} in the {windowLabel}
            </>
          )}
          {by === "child" && <>. Groups are child and downstream pipelines, across every parent.</>}
        </div>
      )}

      <div className="list" role="list">
        {groups.map((grp) => {
          const isOpen = open.has(grp.key);
          const extra = older[grp.key];
          // Older runs start at the newest, so keep only those beyond the window.
          const olderRuns = (extra?.items ?? []).filter((p) => !page?.since || (p.createdAt != null && p.createdAt < page.since));
          const loaded = grp.items || grp.branches;
          const branchKeys = new Set(grp.branches?.map((b) => b.key));
          const strayOlder = grp.branches ? olderRuns.filter((p) => !branchKeys.has(branchKey(grp, p))) : [];
          const runRows = (items: Pipeline[], extraRuns: Pipeline[], depth: number) => {
            const seen = new Set(items.map((p) => p.key));
            return [...items, ...extraRuns.filter((p) => !seen.has(p.key))].map((p) => (
              <PipelineRow
                key={p.key}
                p={p}
                depth={depth}
                showProject={scope.kind === "group"}
                expanded={tree.expanded}
                children={tree.children}
                onToggle={tree.toggle}
                now={now}
              />
            ));
          };
          return (
            <div key={grp.key} role="listitem">
              <GroupRow grp={grp} open={isOpen} onToggle={() => toggleGroup(grp.key)} now={now} />
              {isOpen && !loaded && <div className="row note group-note">Loading runs…</div>}

              {isOpen && grp.items && runRows(grp.items, olderRuns, 1)}
              {isOpen && grp.items && grp.runs > grp.items.length && (
                <div className="row note group-note">Showing the newest {grp.items.length} of {grp.runs} runs.</div>
              )}

              {isOpen &&
                grp.branches?.map((br) => {
                  const brOpen = open.has(br.key);
                  const brOlder = olderRuns.filter((p) => branchKey(grp, p) === br.key);
                  return (
                    <div key={br.key}>
                      <GroupRow grp={br} branch olderCount={brOlder.length} open={brOpen} onToggle={() => toggleGroup(br.key)} now={now} />
                      {brOpen && !br.items && <div className="row note branch-note">Loading runs…</div>}
                      {brOpen && br.items && runRows(br.items, brOlder, 2)}
                      {brOpen && br.items && br.runs > br.items.length && (
                        <div className="row note branch-note">Showing the newest {br.items.length} of {br.runs} runs.</div>
                      )}
                    </div>
                  );
                })}
              {isOpen && strayOlder.length > 0 && (
                <>
                  <div className="row note group-note">Older runs on branches not in this window</div>
                  {runRows([], strayOlder, 1)}
                </>
              )}

              {isOpen && grp.olderName && loaded && (
                <div className="row note group-note">
                  {extra?.error && <span className="error-text">{extra.error} </span>}
                  {extra?.loading ? (
                    "Loading older runs…"
                  ) : extra && extra.next === undefined ? (
                    olderRuns.length ? "No more older runs." : "No older runs."
                  ) : (
                    <button className="link-btn" onClick={() => loadOlder(grp)}>
                      <IconHistory size={13} aria-hidden /> Load older runs of this workflow
                    </button>
                  )}
                  {grp.branches && olderRuns.length > 0 && !extra?.loading && (
                    <span className="dim"> · {olderRuns.length} older run{olderRuns.length === 1 ? "" : "s"} added under their branches</span>
                  )}
                </div>
              )}
            </div>
          );
        })}
        {!loading && !error && page && groups.length === 0 && <div className="row note">No pipelines in this window match these filters.</div>}
        {loading && <div className="row note">Grouping pipelines…</div>}
      </div>
    </>
  );
}

// branchKey is the key of the branch sub-group a run belongs to (see BranchSep on the server).
const BRANCH_SEP = "\x1f";
function branchKey(grp: PipelineGroup, p: Pipeline) {
  return `${grp.key}${BRANCH_SEP}r:${p.branch || p.ref || ""}`;
}

type RowProps = { grp: PipelineGroup; open: boolean; onToggle: () => void; now: number; branch?: boolean; olderCount?: number };

function GroupRow({ grp, open, onToggle, now, branch, olderCount }: RowProps) {
  const finished = grp.passed + grp.failed;
  return (
    <div className={`row group-row${branch ? " branch-row" : ""} tone-border-${toneOf(grp.status)}`} onClick={onToggle}>
      <div className="c-status">
        {grp.limited ? <span className="badge tone-muted">not started</span> : <StatusBadge status={grp.status} label={grp.statusLabel} />}
      </div>
      <div className="c-main">
        <div className="title-line">
          <button
            className="chev"
            onClick={(e) => {
              e.stopPropagation();
              onToggle();
            }}
            aria-expanded={open}
            aria-label={open ? `Collapse ${grp.label}` : `Expand ${grp.label}`}
          >
            {open ? <IconChevronDown size={16} /> : <IconChevronRight size={16} />}
          </button>
          {branch && <IconGitBranch size={14} className="dim" aria-hidden />}
          <span className="title group-title">{grp.label}</span>
          {grp.sub && <span className="chip">{grp.sub}</span>}
          {grp.mergeRequest && grp.label !== `!${grp.mergeRequest.iid}` && <MRChip mr={grp.mergeRequest} showTarget />}
          <span className="dim small">
            {grp.runs} run{grp.runs === 1 ? "" : "s"}
            {olderCount ? ` + ${olderCount} older` : ""}
          </span>
        </div>
      </div>
      <div className="c-history" aria-label="Recent runs, oldest to newest">
        {grp.history.map((h, i) =>
          h.webUrl ? (
            <a
              key={i}
              href={h.webUrl}
              target="_blank"
              rel="noopener noreferrer"
              onClick={(e) => e.stopPropagation()}
              className={`hdot tone-bg-${h.status === "created" ? "muted" : toneOf(h.status)}`}
              title={`#${h.id}: ${h.status}`}
              aria-label={`Pipeline ${h.id}: ${h.status}`}
            />
          ) : (
            <span key={i} className={`hdot tone-bg-${toneOf(h.status)}`} title={`not started: ${h.status}`} />
          ),
        )}
      </div>
      <div className="c-ratio" title={`${grp.passed} passed, ${grp.failed} failed, ${grp.runs - finished} other`}>
        {finished ? `${grp.passed}/${finished} passed` : "–"}
      </div>
      <div className="c-dur" title="Average duration">
        {grp.avgDuration != null ? `avg ${formatDuration(grp.avgDuration)}` : "–"}
      </div>
      <div className="c-age" title={fullTime(grp.lastAt)}>
        {timeAgo(grp.lastAt, now)}
      </div>
    </div>
  );
}

