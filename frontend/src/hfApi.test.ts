import { describe, it, expect, vi, beforeEach } from "vitest";
import { searchModels, getModelDetail, validateToken } from "./hfApi";

const mockFetch = vi.fn();
globalThis.fetch = mockFetch;

beforeEach(() => {
  mockFetch.mockReset();
});

describe("searchModels", () => {
  it("calls HF API with correct params", async () => {
    mockFetch.mockResolvedValueOnce({
      ok: true,
      json: () => Promise.resolve([{ modelId: "meta-llama/Llama-3.1-8B" }]),
    });

    const results = await searchModels("llama");
    expect(mockFetch).toHaveBeenCalledTimes(1);

    const url = mockFetch.mock.calls[0][0] as string;
    expect(url).toContain("search=llama");
    expect(url).toContain("pipeline_tag=text-generation");
    expect(url).toContain("sort=downloads");
    expect(url).toContain("direction=-1");
    expect(url).toContain("limit=15");
    expect(url).not.toContain("expand");

    expect(results).toEqual([{ modelId: "meta-llama/Llama-3.1-8B" }]);
  });

  it("includes auth header when token provided", async () => {
    mockFetch.mockResolvedValueOnce({
      ok: true,
      json: () => Promise.resolve([]),
    });

    await searchModels("llama", "hf_test123");
    const headers = mockFetch.mock.calls[0][1]?.headers as Record<
      string,
      string
    >;
    expect(headers["Authorization"]).toBe("Bearer hf_test123");
  });

  it("does not include auth header when no token", async () => {
    mockFetch.mockResolvedValueOnce({
      ok: true,
      json: () => Promise.resolve([]),
    });

    await searchModels("llama");
    const headers = mockFetch.mock.calls[0][1]?.headers as Record<
      string,
      string
    >;
    expect(headers["Authorization"]).toBeUndefined();
  });

  it("throws on non-ok response", async () => {
    mockFetch.mockResolvedValueOnce({ ok: false, status: 500 });
    await expect(searchModels("test")).rejects.toThrow("HF search failed: 500");
  });

  it("respects custom limit", async () => {
    mockFetch.mockResolvedValueOnce({
      ok: true,
      json: () => Promise.resolve([]),
    });

    await searchModels("test", undefined, 5);
    const url = mockFetch.mock.calls[0][0] as string;
    expect(url).toContain("limit=5");
  });
});

describe("getModelDetail", () => {
  it("calls detail endpoint with model ID", async () => {
    mockFetch.mockResolvedValueOnce({
      ok: true,
      json: () =>
        Promise.resolve({
          modelId: "meta-llama/Llama-3.1-8B",
          sha: "abc123",
        }),
    });

    const detail = await getModelDetail("meta-llama/Llama-3.1-8B");
    const url = mockFetch.mock.calls[0][0] as string;
    expect(url).toContain("/models/meta-llama/Llama-3.1-8B");
    expect(detail.sha).toBe("abc123");
  });

  it("throws 'gated' on 403", async () => {
    mockFetch.mockResolvedValueOnce({ ok: false, status: 403 });
    await expect(
      getModelDetail("meta-llama/Llama-3.1-8B")
    ).rejects.toThrow("gated");
  });

  it("throws generic error on other failures", async () => {
    mockFetch.mockResolvedValueOnce({ ok: false, status: 500 });
    await expect(
      getModelDetail("meta-llama/Llama-3.1-8B")
    ).rejects.toThrow("HF detail failed: 500");
  });

  it("includes auth header when token provided", async () => {
    mockFetch.mockResolvedValueOnce({
      ok: true,
      json: () => Promise.resolve({ modelId: "test", sha: "abc" }),
    });

    await getModelDetail("test/model", "hf_token");
    const headers = mockFetch.mock.calls[0][1]?.headers as Record<
      string,
      string
    >;
    expect(headers["Authorization"]).toBe("Bearer hf_token");
  });
});

describe("validateToken", () => {
  it("returns true on ok response", async () => {
    mockFetch.mockResolvedValueOnce({ ok: true });
    expect(await validateToken("hf_valid")).toBe(true);
  });

  it("returns false on non-ok response", async () => {
    mockFetch.mockResolvedValueOnce({ ok: false });
    expect(await validateToken("hf_invalid")).toBe(false);
  });

  it("calls whoami-v2 with bearer token", async () => {
    mockFetch.mockResolvedValueOnce({ ok: true });
    await validateToken("hf_test");

    const url = mockFetch.mock.calls[0][0] as string;
    expect(url).toContain("/whoami-v2");

    const headers = mockFetch.mock.calls[0][1]?.headers as Record<
      string,
      string
    >;
    expect(headers["Authorization"]).toBe("Bearer hf_test");
  });
});
