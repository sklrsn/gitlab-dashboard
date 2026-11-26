import {
  IconArrowBackUp,
  IconChevronDown,
  IconChevronRight,
  IconExternalLink,
  IconGitBranch,
  IconLock,
  IconSubtask,
} from "@tabler/icons-react";
import type { MRRef, Pipeline } from "./api";
import { elapsedSince, formatDuration, fullTime, timeAgo } from "./format";
import { StatusBadge, toneOf } from "./status";

export type ChildState = { loading: boolean; error?: string; items?: Pipeline[] };

type Props = {
  p: Pipeline;
  depth: number;
  showProject: boolean;
  expanded: Set<string>;
  children: Record<string, ChildState>;
  onToggle: (p: Pipeline) => void;
  now: number;
};

export function PipelineRow({ p, depth, showProject, expanded, children, onToggle, now }: Props) {
  const canExpand = p.childCount > 0 && !p.limited && p.id != null && p.iid != null;
  const isOpen = expanded.has(p.key);
  const kids = children[p.key];
  const running = (p.status === "running" || p.status === "canceling") && p.startedAt;
  const duration = p.duration ?? (running ? elapsedSince(p.startedAt, now) : undefined);
  const shortSha = p.sha?.slice(0, 8);

  return (
    <>
      <div className={`row tone-border-${toneOf(p.status)}${depth ? " child" : ""}`} style={{ paddingLeft: 10 + depth * 26 }}>
        <div className="c-status">
          <StatusBadge status={p.status} label={p.statusLabel} />
        </div>
        <div className="c-main">
          <div className="title-line">
            {canExpand ? (
              <button className="chev" onClick={() => onToggle(p)} aria-expanded={isOpen} aria-label={isOpen ? "Collapse" : "Expand"}>
                {isOpen ? <IconChevronDown size={16} /> : <IconChevronRight size={16} />}
              </button>
            ) : (
              depth > 0 && <span className="tree" aria-hidden>└</span>
            )}
            <a className="title" href={p.webUrl} target="_blank" rel="noopener noreferrer" title="Open in GitLab">
              {p.title}
            </a>
            {p.id != null && <span className="mono dim">#{p.id}</span>}
            {p.triggerJob && p.title !== p.triggerJob && <span className="dim small">via {p.triggerJob}</span>}
            {p.crossProject && <span className="tag tag-downstream">downstream</span>}
            {p.limited && p.id == null && (
              <span className="dim small" title="The trigger job hasn't started its pipeline">
                not started
              </span>
            )}
            {p.limited && p.id != null && (
              <span className="dim small" title="The dashboard token can't read this pipeline's details">
                <IconLock size={13} aria-hidden /> limited
              </span>
            )}
          </div>
          <div className="meta">
            {(showProject || p.crossProject) && p.project.path && (
              <a className="chip" href={p.project.webUrl} target="_blank" rel="noopener noreferrer">
                {p.project.path}
              </a>
            )}
            {(p.branch || p.ref) && !(p.mergeRequest && p.branch === `!${p.mergeRequest.iid}`) && (
              <span className="chip" title={p.ref}>
                <IconGitBranch size={12} aria-hidden />
                {p.branch || p.ref}
              </span>
            )}
            {p.mergeRequest && <MRChip mr={p.mergeRequest} />}
            {shortSha && <span className="mono dim">{shortSha}</span>}
            {p.user && (
              <span className="user" title={`${p.user.name} (@${p.user.username})`}>
                {p.user.avatarUrl && <img src={p.user.avatarUrl} alt="" width={16} height={16} loading="lazy" referrerPolicy="no-referrer" />}
                {p.user.username}
              </span>
            )}
            {p.upstream && depth === 0 && (
              <a className="dim small link" href={p.upstream.webUrl} target="_blank" rel="noopener noreferrer">
                <IconArrowBackUp size={13} aria-hidden /> triggered by {p.upstream.projectPath} #{p.upstream.id}
              </a>
            )}
            {p.parent && (
              <a className="dim small link" href={p.parent.webUrl} target="_blank" rel="noopener noreferrer">
                <IconArrowBackUp size={13} aria-hidden /> from {p.parent.title} #{p.parent.id}
              </a>
            )}
            {canExpand && (
              <button className="link-btn" onClick={() => onToggle(p)}>
                <IconSubtask size={13} aria-hidden /> {p.childCount} {p.childCount === 1 ? "child pipeline" : "child pipelines"}
              </button>
            )}
          </div>
        </div>
        <div className="c-stages" aria-label="Stages">
          {p.stages.map((s) => (
            <span key={s.name} className={`dot tone-bg-${s.status === "created" ? "muted" : toneOf(s.status)}`} title={`${s.name}: ${s.label || s.status}`} />
          ))}
        </div>
        <div className="c-dur" title={p.startedAt ? `Started ${fullTime(p.startedAt)}` : undefined}>
          {formatDuration(duration)}
        </div>
        <div className="c-age" title={fullTime(p.createdAt)}>
          {timeAgo(p.createdAt, now)}
        </div>
        <a className="c-ext" href={p.webUrl} target="_blank" rel="noopener noreferrer" aria-label="Open in GitLab">
          <IconExternalLink size={15} />
        </a>
      </div>
      {isOpen && (
        <ChildRows state={kids} depth={depth + 1} showProject={showProject} expanded={expanded} children={children} onToggle={onToggle} now={now} />
      )}
    </>
  );
}

function ChildRows({ state, depth, ...rest }: Omit<Props, "p"> & { state?: ChildState }) {
  if (!state || (state.loading && !state.items)) {
    return (
      <div className="row note" style={{ paddingLeft: 36 + depth * 26 }}>
        Loading child pipelines…
      </div>
    );
  }
  if (state.error && !state.items) {
    return (
      <div className="row note error" style={{ paddingLeft: 36 + depth * 26 }}>
        {state.error}
      </div>
    );
  }
  if (!state.items?.length) {
    return (
      <div className="row note" style={{ paddingLeft: 36 + depth * 26 }}>
        No child pipelines found.
      </div>
    );
  }
  return (
    <>
      {state.items.map((c) => (
        <PipelineRow key={c.key} p={c} depth={depth} {...rest} />
      ))}
    </>
  );
}

// MRChip links to the merge request a pipeline runs for.
export function MRChip({ mr, showTarget }: { mr: MRRef; showTarget?: boolean }) {
  const label = `!${mr.iid}`;
  const title = mr.title ? `${label}: ${mr.title}` : `Merge request ${label}`;
  return (
    <>
      {mr.webUrl ? (
        <a className="chip chip-mr" href={mr.webUrl} target="_blank" rel="noopener noreferrer" title={title} onClick={(e) => e.stopPropagation()}>
          {label}
        </a>
      ) : (
        <span className="chip chip-mr" title={title}>
          {label}
        </span>
      )}
      {showTarget && mr.targetBranch && <span className="dim small">→ {mr.targetBranch}</span>}
    </>
  );
}
