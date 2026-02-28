const HF_API = "https://huggingface.co/api";

export interface HfModelSummary {
  modelId: string;
  downloads: number;
  likes: number;
  private: boolean;
}

export interface HfModelDetail extends HfModelSummary {
  sha: string;
  gated: boolean | string;
  config?: {
    model_type?: string;
    architectures?: string[];
  };
}

export async function searchModels(
  query: string,
  token?: string,
  limit = 15
): Promise<HfModelSummary[]> {
  const params = new URLSearchParams({
    search: query,
    pipeline_tag: "text-generation",
    sort: "downloads",
    direction: "-1",
    limit: String(limit),
  });

  const headers: Record<string, string> = {};
  if (token) {
    headers["Authorization"] = `Bearer ${token}`;
  }

  const res = await fetch(`${HF_API}/models?${params}`, { headers });
  if (!res.ok) {
    throw new Error(`HF search failed: ${res.status}`);
  }
  return res.json();
}

export async function getModelDetail(
  modelId: string,
  token?: string
): Promise<HfModelDetail> {
  const headers: Record<string, string> = {};
  if (token) {
    headers["Authorization"] = `Bearer ${token}`;
  }

  const res = await fetch(`${HF_API}/models/${modelId}`, { headers });
  if (!res.ok) {
    if (res.status === 403) {
      throw new Error("gated");
    }
    throw new Error(`HF detail failed: ${res.status}`);
  }
  return res.json();
}

export async function validateToken(token: string): Promise<boolean> {
  const res = await fetch(`${HF_API}/whoami-v2`, {
    headers: { Authorization: `Bearer ${token}` },
  });
  return res.ok;
}
