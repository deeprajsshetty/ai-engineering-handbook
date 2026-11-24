import type {
  Catalog,
  Change,
  Facets,
  Fleet,
  CollectionRun,
  Connection,
  Graph,
  Job,
  JobEvent,
  List,
  Me,
  Problem,
  Provider,
  Relationship,
  Resource,
  ResourceMetric,
  Series,
  Subgraph,
  Summary,
} from "./types";

/** ApiError carries the RFC 9457 problem returned by the server. */
export class ApiError extends Error {
  readonly status: number;
  readonly problem?: Problem;
  constructor(status: number, problem?: Problem, message?: string) {
    super(message ?? problem?.detail ?? problem?.title ?? `HTTP ${status}`);
    this.status = status;
    this.problem = problem;
  }
}

const TOKEN_KEY = "obs.devToken";

// Session storage is a per-tab convenience for the development token only.
// It can be unavailable (private mode, blocked storage), so every access is
// guarded and the app falls back to an in-memory token.
let memoryToken: string | null = null;

export function getToken(): string | null {
  try {
    return sessionStorage.getItem(TOKEN_KEY) ?? memoryToken;
  } catch {
    return memoryToken;
  }
}

export function setToken(token: string | null): void {
  memoryToken = token;
  try {
    if (token) sessionStorage.setItem(TOKEN_KEY, token);
    else sessionStorage.removeItem(TOKEN_KEY);
  } catch {
    /* storage unavailable: keep the in-memory copy */
  }
}

type Query = Record<string, string | number | boolean | undefined | null>;

function qs(q?: Query): string {
  if (!q) return "";
  const p = new URLSearchParams();
  for (const [k, v] of Object.entries(q)) {
    if (v !== undefined && v !== null && v !== "") p.set(k, String(v));
  }
  const s = p.toString();
  return s ? `?${s}` : "";
}

export async function request<T>(
  method: string,
  path: string,
  opts: { query?: Query; body?: unknown; headers?: Record<string, string>; signal?: AbortSignal } = {},
): Promise<T> {
  const headers: Record<string, string> = { Accept: "application/json", ...opts.headers };
  const token = getToken();
  if (token) headers.Authorization = `Bearer ${token}`;
  if (opts.body !== undefined) headers["Content-Type"] = "application/json";
  const res = await fetch(`${path}${qs(opts.query)}`, {
    method,
    headers,
    body: opts.body === undefined ? undefined : JSON.stringify(opts.body),
    signal: opts.signal,
  });
  const text = await res.text();
  let data: unknown = undefined;
  if (text) {
    try {
      data = JSON.parse(text);
    } catch {
      data = undefined;
    }
  }
  if (!res.ok) {
    const p = data && typeof data === "object" && "code" in data ? (data as Problem) : undefined;
    throw new ApiError(res.status, p);
  }
  return data as T;
}

const v1 = "/api/v1";

export const api = {
  me: () => request<Me>("GET", `${v1}/me`),
  providers: () => request<List<Provider>>("GET", `${v1}/providers`),
  connections: () => request<List<Connection>>("GET", `${v1}/connections`, { query: { limit: 500 } }),
  createConnection: (body: {
    name: string;
    provider: string;
    config: Record<string, unknown>;
    credentialRef?: string;
    discoveryIntervalMinutes?: number;
  }) => request<Connection>("POST", `${v1}/connections`, { body }),
  validateConnection: (id: string) =>
    request<{ valid: boolean; errors: { field: string; message: string }[]; check: { performed: boolean; ok: boolean; message?: string } }>(
      "POST",
      `${v1}/connections/${id}/validate`,
    ),
  startDiscovery: (connectionId: string, idempotencyKey: string) =>
    request<Job>("POST", `${v1}/connections/${connectionId}/discovery-jobs`, {
      headers: { "Idempotency-Key": idempotencyKey },
    }),
  collectMetrics: (connectionId: string) => request<CollectionRun>("POST", `${v1}/connections/${connectionId}/metric-collections`),
  jobs: (q: { connectionId?: string; status?: string; limit?: number; cursor?: string } = {}) =>
    request<List<Job>>("GET", `${v1}/discovery-jobs`, { query: q }),
  job: (id: string) => request<Job>("GET", `${v1}/discovery-jobs/${id}`),
  jobEvents: (id: string) => request<List<JobEvent>>("GET", `${v1}/discovery-jobs/${id}/events`),
  cancelJob: (id: string) => request<Job>("POST", `${v1}/discovery-jobs/${id}/cancel`),
  catalog: () => request<Catalog>("GET", `${v1}/catalog`),
  facets: (q: Query) => request<Facets>("GET", `${v1}/resources/facets`, { query: q }),
  fleet: (q: Query) => request<Fleet>("GET", `${v1}/fleet`, { query: q }),
  resources: (q: Query) => request<List<Resource>>("GET", `${v1}/resources`, { query: q }),
  resource: (id: string) => request<Resource>("GET", `${v1}/resources/${id}`),
  neighborhood: (id: string, depth = 1) =>
    request<Subgraph>("GET", `${v1}/resources/${id}/neighborhood`, { query: { depth, direction: "both", maxNodes: 60 } }),
  relationships: (q: Query) => request<List<Relationship>>("GET", `${v1}/relationships`, { query: q }),
  summary: (connectionId?: string) => request<Summary>("GET", `${v1}/topology/summary`, { query: { connectionId } }),
  graph: (q: Query) => request<Graph>("GET", `${v1}/topology/graph`, { query: q }),
  changes: (q: Query) => request<List<Change>>("GET", `${v1}/topology/changes`, { query: q }),
  resourceMetrics: (id: string) => request<List<ResourceMetric>>("GET", `${v1}/resources/${id}/metrics`),
  series: (id: string, metric: string, from: string, to?: string) =>
    request<Series>("GET", `${v1}/resources/${id}/metrics/${encodeURIComponent(metric)}`, { query: { from, to } }),
  collections: (connectionId?: string) =>
    request<List<CollectionRun>>("GET", `${v1}/metric-collections`, { query: { connectionId, limit: 20 } }),
};

/** describeError turns any thrown value into a user-facing sentence. */
export function describeError(e: unknown): string {
  if (e instanceof ApiError) {
    const fields = e.problem?.errors?.map((f) => `${f.field}: ${f.message}`).join("; ");
    const base = e.problem?.detail ?? e.message;
    const rid = e.problem?.requestId ? ` (request ${e.problem.requestId})` : "";
    return `${base}${fields ? ` — ${fields}` : ""}${rid}`;
  }
  if (e instanceof Error) return e.message;
  return String(e);
}
