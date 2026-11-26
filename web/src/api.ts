export type Scope = { kind: "group" | "project"; path: string };

export type AppConfig = {
  hasLogo: boolean;
  gitlabUrl: string;
  defaultScope?: Scope;
};

export type ScopeItem = Scope & { name: string; webUrl: string };

export type Stage = { name: string; status: string; label: string };

export type MRRef = { iid: number; title?: string; sourceBranch?: string; targetBranch?: string; webUrl?: string };

export type Pipeline = {
  key: string;
  id?: number;
  iid?: string;
  name?: string;
  title: string;
  status: string;
  statusLabel: string;
  source?: string;
  ref?: string;
  branch?: string;
  mergeRequest?: MRRef;
  sha?: string;
  webUrl: string;
  project: { path: string; name: string; webUrl: string };
  createdAt?: string;
  startedAt?: string;
  finishedAt?: string;
  duration?: number;
  user?: { username: string; name: string; avatarUrl?: string };
  commitTitle?: string;
  stages: Stage[];
  childCount: number;
  upstream?: { id: number; projectPath: string; webUrl: string };
  triggerJob?: string;
  crossProject?: boolean;
  limited?: boolean;
  parent?: { id: number; title: string; webUrl: string };
};

export type PipelineGroup = {
  key: string;
  label: string;
  sub?: string;
  status: string;
  statusLabel: string;
  limited?: boolean;
  lastAt?: string;
  runs: number;
  passed: number;
  failed: number;
  avgDuration?: number;
  history: { status: string; id?: number; webUrl?: string }[];
  olderProject?: string;
  olderName?: string;
  mergeRequest?: MRRef;
  branches?: PipelineGroup[];
  items?: Pipeline[];
};

export type GroupedPage = {
  groups: PipelineGroup[];
  pipelines: number;
  since?: string;
  capped: boolean;
  warnings?: string[];
};

export type Page = {
  pipelines: Pipeline[];
  nextCursor?: string;
  hasMore: boolean;
  projects?: number;
  warnings?: string[];
};

export class ApiError extends Error {}

type Params = Record<string, string | string[] | undefined>;

async function get<T>(path: string, params: Params = {}, signal?: AbortSignal): Promise<T> {
  const qs = new URLSearchParams();
  for (const [k, v] of Object.entries(params)) {
    if (Array.isArray(v)) v.forEach((x) => qs.append(k, x));
    else if (v) qs.set(k, v);
  }
  const url = qs.size ? `${path}?${qs}` : path;
  let res: Response;
  try {
    res = await fetch(url, { signal, headers: { Accept: "application/json" } });
  } catch (e) {
    if ((e as Error).name === "AbortError") throw e;
    throw new ApiError("Can't reach the dashboard server.");
  }
  const body = await res.json().catch(() => null);
  if (!res.ok) throw new ApiError(body?.error ?? `Request failed (HTTP ${res.status})`);
  return body as T;
}

export const api = {
  config: () => get<AppConfig>("/api/config"),
  scopes: (search: string, signal?: AbortSignal) => get<ScopeItem[]>("/api/scopes", { search }, signal),
  pipelines: (
    scope: Scope,
    f: { ref?: string; status?: string; cursor?: string; limit?: number },
    signal?: AbortSignal,
  ) =>
    get<Page>(
      "/api/pipelines",
      { [scope.kind]: scope.path, ref: f.ref, status: f.status, cursor: f.cursor, limit: f.limit ? String(f.limit) : undefined },
      signal,
    ),
  scope: (scope: Scope, signal?: AbortSignal) => get<ScopeItem>("/api/scope", { [scope.kind]: scope.path }, signal),
  groups: (
    scope: Scope,
    f: { by: string; window?: string; ref?: string; status?: string; open?: string[] },
    signal?: AbortSignal,
  ) =>
    get<GroupedPage>(
      "/api/groups",
      { [scope.kind]: scope.path, by: f.by, window: f.window, ref: f.ref, status: f.status, open: f.open },
      signal,
    ),
  runs: (project: string, name: string, page: number, signal?: AbortSignal) =>
    get<Page>("/api/runs", { project, name, page: String(page) }, signal),
  children: (p: Pipeline, signal?: AbortSignal) =>
    get<Pipeline[]>("/api/children", { project: p.project.path, iid: p.iid, id: String(p.id) }, signal),
};
