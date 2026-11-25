export type Scope = { kind: "group" | "project"; path: string };

export type AppConfig = {
  title: string;
  hasLogo: boolean;
  gitlabUrl: string;
  defaultScope?: Scope;
};

export type ScopeItem = Scope & { name: string; webUrl: string };

export type Stage = { name: string; status: string; label: string };

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
};

export type Page = {
  pipelines: Pipeline[];
  nextCursor?: string;
  hasMore: boolean;
  projects?: number;
  warnings?: string[];
};

export class ApiError extends Error {}

async function get<T>(path: string, params: Record<string, string | undefined> = {}, signal?: AbortSignal): Promise<T> {
  const qs = new URLSearchParams();
  for (const [k, v] of Object.entries(params)) if (v) qs.set(k, v);
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
  children: (p: Pipeline, signal?: AbortSignal) =>
    get<Pipeline[]>("/api/children", { project: p.project.path, iid: p.iid, id: String(p.id) }, signal),
};
