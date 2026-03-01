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
  PricingRow,
  RecommendResponse,
} from "./types";

const BASE = "/api/v1";

async function fetchJSON<T>(url: string, init?: RequestInit): Promise<T> {
  const res = await fetch(url, init);
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

export async function listCatalog(
  filter: CatalogFilter = {}
): Promise<CatalogEntry[]> {
  const params = new URLSearchParams();
  if (filter.model) params.set("model", filter.model);
  if (filter.model_family) params.set("model_family", filter.model_family);
  if (filter.instance_family)
    params.set("instance_family", filter.instance_family);
  if (filter.accelerator_type)
    params.set("accelerator_type", filter.accelerator_type);
  if (filter.sort) params.set("sort", filter.sort);
  if (filter.order) params.set("order", filter.order);
  if (filter.limit) params.set("limit", String(filter.limit));
  if (filter.offset) params.set("offset", String(filter.offset));

  const qs = params.toString();
  return fetchJSON<CatalogEntry[]>(`${BASE}/catalog${qs ? `?${qs}` : ""}`);
}

export async function getRun(id: string): Promise<BenchmarkRun> {
  return fetchJSON<BenchmarkRun>(`${BASE}/runs/${id}`);
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

export async function listRuns(
  filter: RunListFilter = {}
): Promise<RunListItem[]> {
  const params = new URLSearchParams();
  if (filter.status) params.set("status", filter.status);
  if (filter.model) params.set("model", filter.model);
  if (filter.limit) params.set("limit", String(filter.limit));
  if (filter.offset) params.set("offset", String(filter.offset));

  const qs = params.toString();
  return fetchJSON<RunListItem[]>(`${BASE}/jobs${qs ? `?${qs}` : ""}`);
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
  hfToken?: string
): Promise<RecommendResponse> {
  const params = new URLSearchParams({ model, instance_type: instanceType });
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
