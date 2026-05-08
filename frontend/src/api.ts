import type {
  CatalogEntry,
  CatalogFilter,
  CatalogSeedStatus,
  BenchmarkRun,
  BenchmarkMetrics,
  InstanceType,
  RunRequest,
  RunListItem,
  RunListFilter,
  Job,
  JobFilter,
  ModelCacheFilter,
  PricingRow,
  RecommendResponse,
  EstimateFilter,
  EstimateResponse,
  MemoryBreakdownResponse,
  OOMHistory,
  Scenario,
  TestSuite,
  SuiteRunRequest,
  TestSuiteRun,
  ModelCache,
  CacheModelRequest,
  RegisterCustomModelRequest,
  StatusResponse,
  CredentialsStatus,
  CatalogMatrixPayload,
  ToolVersions,
  ScenarioOverrideEntry,
  ScenarioOverride,
  RegistryStatus,
  AuditLogEntry,
  NodePoolReservations,
  DashboardStats,
  ModelCacheStats,
  RunDetailResponse,
  SuiteDetailResponse,
  AuthUser,
  CognitoUser,
  ListUsersResponse,
} from "./types";
import type { Paginated } from "./lib/pagination";

const BASE = "/api/v1";

export async function getStatus(): Promise<StatusResponse> {
  return fetchJSON<StatusResponse>(`${BASE}/status`);
}

// PRD-43: all API calls carry HttpOnly auth cookies via credentials:"include".
// On a 401 response, fetchJSON attempts exactly one silent refresh; if that
// succeeds, the original request is retried once. If the refresh returns 401
// too, we redirect the user to /login (except for the auth endpoints
// themselves, which handle 401 as a "bad credentials" UX state).
async function fetchJSON<T>(url: string, init?: RequestInit): Promise<T> {
  const withCreds: RequestInit = { credentials: "include", ...(init ?? {}) };
  let res = await fetch(url, withCreds);

  if (res.status === 401 && !isAuthEndpoint(url)) {
    const refreshed = await trySilentRefresh();
    if (refreshed) {
      res = await fetch(url, withCreds);
    } else {
      redirectToLogin();
      throw new Error("unauthorized");
    }
  }

  if (!res.ok) {
    const body = await res.text();
    let message = body;
    try {
      const parsed = JSON.parse(body);
      if (parsed.error) message = parsed.error;
    } catch {
      // body is not JSON, use as-is
    }
    throw new Error(message);
  }
  return res.json();
}

function isAuthEndpoint(url: string): boolean {
  return (
    url.includes("/auth/login") ||
    url.includes("/auth/refresh") ||
    url.includes("/auth/logout") ||
    url.includes("/auth/respond-challenge")
  );
}

// trySilentRefresh returns true if the refresh call returned 2xx.
async function trySilentRefresh(): Promise<boolean> {
  try {
    const res = await fetch(`${BASE}/auth/refresh`, {
      method: "POST",
      credentials: "include",
    });
    return res.ok;
  } catch {
    return false;
  }
}

function redirectToLogin() {
  if (typeof window !== "undefined" && window.location.pathname !== "/login") {
    window.location.href = "/login";
  }
}

// PRD-43: auth endpoints.

// authLogin returns a discriminated union: either an authenticated user
// (the common case) or a challenge. Invited users hit the challenge path
// with Cognito's NEW_PASSWORD_REQUIRED on first sign-in; the UI shows a
// "set new password" form and posts the answer to authRespondChallenge.
export type LoginChallenge = {
  challenge: "new_password_required";
  session: string;
  email: string;
};

export type LoginResult =
  | { kind: "user"; user: AuthUser }
  | { kind: "challenge"; challenge: LoginChallenge };

export async function authLogin(email: string, password: string): Promise<LoginResult> {
  const body = await fetchJSON<AuthUser | LoginChallenge>(`${BASE}/auth/login`, {
    method: "POST",
    headers: { "Content-Type": "application/json" },
    body: JSON.stringify({ email, password }),
  });
  if ("challenge" in body) {
    return { kind: "challenge", challenge: body };
  }
  return { kind: "user", user: body };
}

export async function authRespondChallenge(
  challenge: LoginChallenge,
  newPassword: string,
): Promise<AuthUser> {
  return fetchJSON<AuthUser>(`${BASE}/auth/respond-challenge`, {
    method: "POST",
    headers: { "Content-Type": "application/json" },
    body: JSON.stringify({
      challenge: challenge.challenge,
      session: challenge.session,
      email: challenge.email,
      new_password: newPassword,
    }),
  });
}

export async function authLogout(): Promise<void> {
  await fetch(`${BASE}/auth/logout`, {
    method: "POST",
    credentials: "include",
  });
}

export async function authMe(): Promise<AuthUser> {
  return fetchJSON<AuthUser>(`${BASE}/auth/me`);
}

export async function authRefresh(): Promise<boolean> {
  return trySilentRefresh();
}

// PRD-36: /api/v1/catalog now returns { rows, total } so paginated UIs can
// render "showing X-Y of Z" without a second query.
export async function listCatalog(
  filter: CatalogFilter = {}
): Promise<Paginated<CatalogEntry>> {
  const params = new URLSearchParams();
  if (filter.ids && filter.ids.length > 0) params.set("ids", filter.ids.join(","));
  if (filter.model) params.set("model", filter.model);
  if (filter.model_type) params.set("model_type", filter.model_type);
  if (filter.instance_family)
    params.set("instance_family", filter.instance_family);
  if (filter.accelerator_type)
    params.set("accelerator_type", filter.accelerator_type);
  if (filter.sort) params.set("sort", filter.sort);
  if (filter.order) params.set("order", filter.order);
  if (filter.limit) params.set("limit", String(filter.limit));
  if (filter.offset) params.set("offset", String(filter.offset));

  const qs = params.toString();
  return fetchJSON<Paginated<CatalogEntry>>(`${BASE}/catalog${qs ? `?${qs}` : ""}`);
}

export async function getRun(id: string): Promise<BenchmarkRun> {
  return fetchJSON<BenchmarkRun>(`${BASE}/runs/${id}`);
}

export async function getRunDetail(
  id: string,
  includes: string[]
): Promise<RunDetailResponse> {
  return fetchJSON<RunDetailResponse>(
    `${BASE}/runs/${id}?include=${includes.join(",")}`
  );
}

export async function getMetrics(runId: string): Promise<BenchmarkMetrics> {
  return fetchJSON<BenchmarkMetrics>(`${BASE}/runs/${runId}/metrics`);
}

export async function createRun(
  req: RunRequest
): Promise<{ id: string; status: string }> {
  return fetchJSON(`${BASE}/runs`, {
    method: "POST",
    headers: { "Content-Type": "application/json" },
    body: JSON.stringify(req),
  });
}

// PRD-36: /api/v1/jobs now returns a unified feed of single + suite runs as
// { rows, total }. Use listJobs() for new callers; listRuns() is kept for
// backwards compatibility with Dashboard which hasn't migrated yet.
export async function listJobs(filter: JobFilter = {}): Promise<Paginated<Job>> {
  const params = new URLSearchParams();
  if (filter.type) params.set("type", filter.type);
  if (filter.status) params.set("status", filter.status);
  if (filter.model) params.set("model", filter.model);
  if (filter.sort) params.set("sort", filter.sort);
  if (filter.order) params.set("order", filter.order);
  if (filter.limit) params.set("limit", String(filter.limit));
  if (filter.offset) params.set("offset", String(filter.offset));

  const qs = params.toString();
  return fetchJSON<Paginated<Job>>(`${BASE}/jobs${qs ? `?${qs}` : ""}`);
}

// Legacy wrapper: old callers (Dashboard) expected a bare array of runs.
// Filters the unified jobs feed down to type="run" and returns only rows.
// New code should prefer listJobs().
export async function listRuns(
  filter: RunListFilter = {}
): Promise<RunListItem[]> {
  const jobs = await listJobs({ ...filter, type: "run" });
  return jobs.rows.map((j) => ({
    id: j.id,
    model_hf_id: j.model_hf_id,
    instance_type_name: j.instance_type_name,
    framework: j.framework_or_suite,
    run_type: "",
    status: j.status,
    error_message: j.error_message,
    created_at: j.created_at,
    started_at: j.started_at,
    completed_at: j.completed_at,
  }));
}

export async function listInstanceTypes(): Promise<InstanceType[]> {
  return fetchJSON<InstanceType[]>(`${BASE}/instance-types`);
}

export async function listPricing(region?: string): Promise<PricingRow[]> {
  const params = new URLSearchParams();
  if (region) params.set("region", region);
  const qs = params.toString();
  return fetchJSON<PricingRow[]>(`${BASE}/pricing${qs ? `?${qs}` : ""}`);
}

export async function getRecommendation(
  model: string,
  instanceType: string,
  hfToken?: string,
  tp?: number,
  overheadGiB?: number,
  maxModelLen?: number
): Promise<RecommendResponse> {
  const params = new URLSearchParams({ model, instance_type: instanceType });
  if (tp !== undefined && tp > 0) params.set("tp", String(tp));
  if (overheadGiB !== undefined && overheadGiB > 0) params.set("overhead_gib", String(overheadGiB));
  if (maxModelLen !== undefined && maxModelLen > 0) params.set("max_model_len", String(maxModelLen));
  const headers: Record<string, string> = {};
  if (hfToken) headers["X-HF-Token"] = hfToken;
  return fetchJSON<RecommendResponse>(`${BASE}/recommend?${params}`, { headers });
}

export async function seedCatalog(): Promise<{ job_name: string; status: string }> {
  return fetchJSON(`${BASE}/catalog/seed`, { method: "POST" });
}

export async function getCatalogSeedStatus(): Promise<CatalogSeedStatus> {
  return fetchJSON<CatalogSeedStatus>(`${BASE}/catalog/seed`);
}

export async function cancelRun(id: string): Promise<void> {
  await fetch(`${BASE}/runs/${id}/cancel`, { method: "POST" });
}

export async function deleteRun(id: string): Promise<void> {
  await fetch(`${BASE}/runs/${id}`, { method: "DELETE" });
}

export async function getEstimate(
  model: string,
  filter: EstimateFilter = {},
  hfToken?: string
): Promise<EstimateResponse> {
  const params = new URLSearchParams({ model });
  if (filter.accelerator_type) params.set("accelerator_type", filter.accelerator_type);
  if (filter.max_cost_hourly) params.set("max_cost_hourly", String(filter.max_cost_hourly));
  if (filter.min_context_length) params.set("min_context_length", String(filter.min_context_length));
  if (filter.quantization) params.set("quantization", filter.quantization);
  if (filter.region) params.set("region", filter.region);

  const headers: Record<string, string> = {};
  if (hfToken) headers["X-HF-Token"] = hfToken;

  return fetchJSON<EstimateResponse>(`${BASE}/estimate?${params}`, { headers });
}

// PRD-15: Memory breakdown
export interface MemoryBreakdownParams {
  model: string;
  instanceType: string;
  tp?: number;
  quantization?: string;
  maxModelLen?: number;
  inputSeqLen?: number;
  outputSeqLen?: number;
  concurrency?: number;
  overheadGiB?: number;
  hfToken?: string;
}

export async function getMemoryBreakdown(
  params: MemoryBreakdownParams
): Promise<MemoryBreakdownResponse> {
  const urlParams = new URLSearchParams({
    model: params.model,
    instance_type: params.instanceType,
  });
  if (params.tp) urlParams.set("tp", String(params.tp));
  if (params.quantization) urlParams.set("quantization", params.quantization);
  if (params.maxModelLen) urlParams.set("max_model_len", String(params.maxModelLen));
  if (params.inputSeqLen) urlParams.set("input_seq_len", String(params.inputSeqLen));
  if (params.outputSeqLen) urlParams.set("output_seq_len", String(params.outputSeqLen));
  if (params.concurrency) urlParams.set("concurrency", String(params.concurrency));
  if (params.overheadGiB) urlParams.set("overhead_gib", String(params.overheadGiB));

  const headers: Record<string, string> = {};
  if (params.hfToken) headers["X-HF-Token"] = params.hfToken;

  return fetchJSON<MemoryBreakdownResponse>(`${BASE}/memory-breakdown?${urlParams}`, { headers });
}

// PRD-15: OOM history
export async function getOOMHistory(
  model: string,
  instanceType: string,
  limit?: number
): Promise<OOMHistory> {
  const params = new URLSearchParams({ model, instance_type: instanceType });
  if (limit) params.set("limit", String(limit));
  return fetchJSON<OOMHistory>(`${BASE}/oom-history?${params}`);
}

// Export Kubernetes manifest for a single run
export function getExportManifestUrl(runId: string): string {
  return `${BASE}/runs/${runId}/export`;
}

// PRD-41: CSV and suite exports.
export function getRunCSVUrl(runId: string): string {
  return `${BASE}/runs/${runId}/csv`;
}
export function getSuiteCSVUrl(suiteId: string): string {
  return `${BASE}/suite-runs/${suiteId}/csv`;
}
export function getSuiteExportManifestUrl(suiteId: string): string {
  return `${BASE}/suite-runs/${suiteId}/export`;
}

// PRD-12: Scenarios
export async function listScenarios(): Promise<Scenario[]> {
  return fetchJSON<Scenario[]>(`${BASE}/scenarios`);
}

// PRD-13: Test Suites
export async function listTestSuites(): Promise<TestSuite[]> {
  return fetchJSON<TestSuite[]>(`${BASE}/test-suites`);
}

export async function createSuiteRun(req: SuiteRunRequest): Promise<TestSuiteRun> {
  return fetchJSON<TestSuiteRun>(`${BASE}/suite-runs`, {
    method: "POST",
    headers: { "Content-Type": "application/json" },
    body: JSON.stringify(req),
  });
}

export async function getSuiteRun(id: string): Promise<TestSuiteRun> {
  return fetchJSON<TestSuiteRun>(`${BASE}/suite-runs/${id}`);
}

export async function getSuiteDetail(
  id: string,
  includes: string[]
): Promise<SuiteDetailResponse> {
  return fetchJSON<SuiteDetailResponse>(
    `${BASE}/suite-runs/${id}?include=${includes.join(",")}`
  );
}

// Suite run list item (denormalized for display)
export interface SuiteRunListItem {
  id: string;
  model_hf_id: string;
  instance_type_name: string;
  suite_id: string;
  status: string;
  created_at: string;
  started_at?: string;
  completed_at?: string;
}

export async function listSuiteRuns(): Promise<SuiteRunListItem[]> {
  return fetchJSON<SuiteRunListItem[]>(`${BASE}/suite-runs`);
}

// PRD-21: Model Cache
// PRD-36: response wrapped as { rows, total }. Autocomplete callers pass no
// filter and get every row; the ModelCache page passes pagination params.
export async function listModelCache(
  filter: ModelCacheFilter = {}
): Promise<Paginated<ModelCache>> {
  const params = new URLSearchParams();
  if (filter.status) params.set("status", filter.status);
  if (filter.sort) params.set("sort", filter.sort);
  if (filter.order) params.set("order", filter.order);
  if (filter.limit) params.set("limit", String(filter.limit));
  if (filter.offset) params.set("offset", String(filter.offset));
  const qs = params.toString();
  return fetchJSON<Paginated<ModelCache>>(`${BASE}/model-cache${qs ? `?${qs}` : ""}`);
}

export async function createModelCache(req: CacheModelRequest): Promise<{ id: string; status: string }> {
  return fetchJSON(`${BASE}/model-cache`, {
    method: "POST",
    headers: { "Content-Type": "application/json" },
    body: JSON.stringify(req),
  });
}

export async function getModelCache(id: string): Promise<ModelCache> {
  return fetchJSON<ModelCache>(`${BASE}/model-cache/${id}`);
}

export async function deleteModelCache(id: string): Promise<void> {
  await fetch(`${BASE}/model-cache/${id}`, { method: "DELETE" });
}

export async function registerCustomModel(req: RegisterCustomModelRequest): Promise<ModelCache> {
  return fetchJSON<ModelCache>(`${BASE}/model-cache/register`, {
    method: "POST",
    headers: { "Content-Type": "application/json" },
    body: JSON.stringify(req),
  });
}

// PRD-31: Configuration / Credentials
export async function getCredentials(): Promise<CredentialsStatus> {
  return fetchJSON<CredentialsStatus>(`${BASE}/config/credentials`);
}

export async function putHFToken(token: string): Promise<void> {
  const res = await fetch(`${BASE}/config/credentials/hf-token`, {
    method: "PUT",
    headers: { "Content-Type": "application/json" },
    body: JSON.stringify({ token }),
  });
  if (!res.ok) {
    const body = await res.text();
    throw new Error(body || `PUT hf-token failed: ${res.status}`);
  }
}

export async function deleteHFToken(): Promise<void> {
  const res = await fetch(`${BASE}/config/credentials/hf-token`, { method: "DELETE" });
  if (!res.ok) throw new Error(`DELETE hf-token failed: ${res.status}`);
}

export async function putDockerHubToken(username: string, access_token: string): Promise<void> {
  const res = await fetch(`${BASE}/config/credentials/dockerhub-token`, {
    method: "PUT",
    headers: { "Content-Type": "application/json" },
    body: JSON.stringify({ username, access_token }),
  });
  if (!res.ok) {
    const body = await res.text();
    throw new Error(body || `PUT dockerhub-token failed: ${res.status}`);
  }
}

export async function deleteDockerHubToken(): Promise<void> {
  const res = await fetch(`${BASE}/config/credentials/dockerhub-token`, { method: "DELETE" });
  if (!res.ok) throw new Error(`DELETE dockerhub-token failed: ${res.status}`);
}

// PRD-35: aggregate stats endpoints.
export async function getDashboardStats(): Promise<DashboardStats> {
  return fetchJSON<DashboardStats>(`${BASE}/dashboard/stats`);
}

export async function getModelCacheStats(): Promise<ModelCacheStats> {
  return fetchJSON<ModelCacheStats>(`${BASE}/model-cache/stats`);
}

// PRD-34: tool versions (vLLM + inference-perf)
export async function getToolVersions(): Promise<ToolVersions> {
  return fetchJSON<ToolVersions>(`${BASE}/config/tool-versions`);
}

export async function putToolVersions(payload: {
  framework_version: string;
  inference_perf_version: string;
}): Promise<ToolVersions> {
  const res = await fetch(`${BASE}/config/tool-versions`, {
    method: "PUT",
    headers: { "Content-Type": "application/json" },
    body: JSON.stringify(payload),
  });
  if (!res.ok) {
    const body = await res.text();
    throw new Error(body || `PUT tool-versions failed: ${res.status}`);
  }
  return res.json();
}

// PRD-32: catalog matrix editor
export async function getCatalogMatrix(): Promise<CatalogMatrixPayload> {
  return fetchJSON<CatalogMatrixPayload>(`${BASE}/config/catalog-matrix`);
}

export async function putCatalogMatrix(payload: CatalogMatrixPayload): Promise<CatalogMatrixPayload> {
  const res = await fetch(`${BASE}/config/catalog-matrix`, {
    method: "PUT",
    headers: { "Content-Type": "application/json" },
    body: JSON.stringify(payload),
  });
  if (!res.ok) {
    const body = await res.text();
    if (res.status === 409) {
      throw new Error("CONFLICT: " + (body || "catalog matrix modified elsewhere"));
    }
    throw new Error(body || `PUT catalog-matrix failed: ${res.status}`);
  }
  return res.json();
}

// PRD-32: scenario overrides
export async function listScenarioOverrides(): Promise<ScenarioOverrideEntry[]> {
  return fetchJSON<ScenarioOverrideEntry[]>(`${BASE}/config/scenario-overrides`);
}

export async function putScenarioOverride(
  scenarioID: string,
  override: Partial<Omit<ScenarioOverride, "scenario_id" | "updated_at">>,
): Promise<void> {
  const res = await fetch(`${BASE}/config/scenario-overrides/${encodeURIComponent(scenarioID)}`, {
    method: "PUT",
    headers: { "Content-Type": "application/json" },
    body: JSON.stringify(override),
  });
  if (!res.ok) {
    const body = await res.text();
    throw new Error(body || `PUT scenario-override failed: ${res.status}`);
  }
}

export async function deleteScenarioOverride(scenarioID: string): Promise<void> {
  const res = await fetch(`${BASE}/config/scenario-overrides/${encodeURIComponent(scenarioID)}`, {
    method: "DELETE",
  });
  if (!res.ok) {
    throw new Error(`DELETE scenario-override failed: ${res.status}`);
  }
}

// PRD-32: registry card
export async function getRegistry(): Promise<RegistryStatus> {
  return fetchJSON<RegistryStatus>(`${BASE}/config/registry`);
}

// PRD-32: audit log
export async function listAuditLog(limit = 50): Promise<AuditLogEntry[]> {
  return fetchJSON<AuditLogEntry[]>(`${BASE}/config/audit-log?limit=${limit}`);
}

// PRD-33: capacity reservations
export async function listCapacityReservations(): Promise<NodePoolReservations[]> {
  return fetchJSON<NodePoolReservations[]>(`${BASE}/config/capacity-reservations`);
}

export async function attachCapacityReservation(
  node_class: string,
  reservation_id: string,
): Promise<void> {
  const res = await fetch(`${BASE}/config/capacity-reservations`, {
    method: "POST",
    headers: { "Content-Type": "application/json" },
    body: JSON.stringify({ node_class, reservation_id }),
  });
  if (!res.ok) {
    const body = await res.text();
    throw new Error(body || `POST capacity-reservations failed: ${res.status}`);
  }
}

export async function detachCapacityReservation(
  node_class: string,
  reservation_id: string,
): Promise<void> {
  const res = await fetch(
    `${BASE}/config/capacity-reservations/${encodeURIComponent(node_class)}/${encodeURIComponent(reservation_id)}`,
    { method: "DELETE" },
  );
  if (!res.ok) {
    const body = await res.text();
    throw new Error(body || `DELETE capacity-reservation failed: ${res.status}`);
  }
}

// ---------- PRD-45: user management ----------

export async function listUsers(opts: {
  limit?: number;
  next_token?: string;
  filter?: string;
} = {}): Promise<ListUsersResponse> {
  const params = new URLSearchParams();
  if (opts.limit) params.set("limit", String(opts.limit));
  if (opts.next_token) params.set("next_token", opts.next_token);
  if (opts.filter) params.set("filter", opts.filter);
  const qs = params.toString();
  return fetchJSON<ListUsersResponse>(`${BASE}/users${qs ? `?${qs}` : ""}`);
}

export async function createUser(email: string, role: "admin" | "user"): Promise<CognitoUser> {
  return fetchJSON<CognitoUser>(`${BASE}/users`, {
    method: "POST",
    headers: { "Content-Type": "application/json" },
    body: JSON.stringify({ email, role }),
  });
}

export async function updateUserRole(sub: string, role: "admin" | "user"): Promise<CognitoUser> {
  return fetchJSON<CognitoUser>(`${BASE}/users/${encodeURIComponent(sub)}`, {
    method: "PATCH",
    headers: { "Content-Type": "application/json" },
    body: JSON.stringify({ role }),
  });
}

export async function disableUser(sub: string): Promise<CognitoUser> {
  return fetchJSON<CognitoUser>(`${BASE}/users/${encodeURIComponent(sub)}/disable`, {
    method: "POST",
  });
}

export async function enableUser(sub: string): Promise<CognitoUser> {
  return fetchJSON<CognitoUser>(`${BASE}/users/${encodeURIComponent(sub)}/enable`, {
    method: "POST",
  });
}

export async function resetUserPassword(sub: string): Promise<CognitoUser> {
  return fetchJSON<CognitoUser>(`${BASE}/users/${encodeURIComponent(sub)}/reset-password`, {
    method: "POST",
  });
}

export async function deleteUser(sub: string): Promise<void> {
  const res = await fetch(`${BASE}/users/${encodeURIComponent(sub)}`, {
    method: "DELETE",
    credentials: "include",
  });
  if (!res.ok) {
    const body = await res.text();
    throw new Error(body || `DELETE /users failed: ${res.status}`);
  }
}
