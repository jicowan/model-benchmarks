#!/usr/bin/env python3
"""AccelBench load generator.

Sends concurrent streaming requests to a vLLM-compatible /v1/completions
endpoint, measures TTFT / E2E latency / ITL, and prints a JSON report
matching the LoadgenOutput schema expected by the metrics collector.

Supports two dataset modes:
  - "sharegpt": Downloads real conversational prompts from the ShareGPT
    dataset on HuggingFace, filtered by approximate token length.
  - "synthetic": Repeats "Hello " to fill the input sequence length.
"""

import asyncio
import json
import os
import random
import sys
import time
import urllib.request
from dataclasses import dataclass, asdict

import aiohttp

# ---------------------------------------------------------------------------
# Config from environment
# ---------------------------------------------------------------------------
TARGET_URL = os.environ["TARGET_URL"]           # e.g. http://bench-xxx:8000/v1/completions
MODEL_ID = os.environ["MODEL_ID"]
CONCURRENCY = int(os.environ.get("CONCURRENCY", "16"))
INPUT_SEQ_LEN = int(os.environ.get("INPUT_SEQ_LEN", "512"))
OUTPUT_SEQ_LEN = int(os.environ.get("OUTPUT_SEQ_LEN", "256"))
NUM_REQUESTS = int(os.environ.get("NUM_REQUESTS", "200"))
WARMUP_REQUESTS = int(os.environ.get("WARMUP_REQUESTS", "10"))
DATASET = os.environ.get("DATASET", "sharegpt")

SHAREGPT_URL = (
    "https://huggingface.co/datasets/anon8231489123/"
    "ShareGPT_Vicuna_unfiltered/resolve/main/"
    "ShareGPT_V3_unfiltered_cleaned_split.json"
)

# Approximate tokens per character for English text.
CHARS_PER_TOKEN = 4


def _estimate_tokens(text: str) -> int:
    """Rough token count estimate: ~4 chars per token for English."""
    return max(1, len(text) // CHARS_PER_TOKEN)


def _load_sharegpt_prompts(data: list[dict], target_tokens: int) -> list[str]:
    """Extract human prompts from ShareGPT conversations, filtered by length.

    Keeps prompts whose estimated token count is between 0.25x and 2x the
    target, then truncates or pads to approximate the target length.
    """
    prompts = []
    min_tokens = max(1, target_tokens // 4)
    max_tokens = target_tokens * 2

    for entry in data:
        conversations = entry.get("conversations", [])
        # Take the first human turn as the prompt.
        for turn in conversations:
            if turn.get("from") == "human":
                text = turn.get("value", "").strip()
                if not text:
                    break
                est = _estimate_tokens(text)
                if min_tokens <= est <= max_tokens:
                    # Truncate to approximate target token count.
                    char_limit = target_tokens * CHARS_PER_TOKEN
                    prompts.append(text[:char_limit])
                break  # only use first human turn per conversation

    return prompts


def download_sharegpt() -> list[str]:
    """Download ShareGPT dataset and return filtered prompts."""
    print(f"Downloading ShareGPT dataset...", file=sys.stderr)
    with urllib.request.urlopen(SHAREGPT_URL, timeout=120) as resp:
        raw = resp.read()

    print(f"Downloaded {len(raw) / 1024 / 1024:.1f} MB, parsing...", file=sys.stderr)
    data = json.loads(raw)
    del raw  # free memory

    prompts = _load_sharegpt_prompts(data, INPUT_SEQ_LEN)
    del data  # free memory

    print(f"Filtered {len(prompts)} prompts matching ~{INPUT_SEQ_LEN} tokens", file=sys.stderr)
    return prompts


def build_prompts() -> list[str]:
    """Build the prompt pool based on the DATASET config."""
    if DATASET == "synthetic":
        print(f"Using synthetic prompts ('Hello ' x {INPUT_SEQ_LEN})", file=sys.stderr)
        return ["Hello " * INPUT_SEQ_LEN]

    # Default: try ShareGPT, fall back to synthetic.
    try:
        prompts = download_sharegpt()
        if len(prompts) >= 10:
            return prompts
        print(f"Only {len(prompts)} ShareGPT prompts matched, falling back to synthetic", file=sys.stderr)
    except Exception as e:
        print(f"Failed to load ShareGPT dataset: {e}", file=sys.stderr)
        print("Falling back to synthetic prompts", file=sys.stderr)

    return ["Hello " * INPUT_SEQ_LEN]


# Build the prompt pool at startup.
PROMPT_POOL: list[str] = []


@dataclass
class RequestResult:
    ttft_ms: float
    e2e_latency_ms: float
    itl_ms: float
    output_tokens: int
    input_tokens: int
    duration_seconds: float
    success: bool


async def send_request(session: aiohttp.ClientSession, semaphore: asyncio.Semaphore) -> RequestResult:
    """Send a single streaming completion request and measure latencies."""
    prompt = random.choice(PROMPT_POOL)
    payload = {
        "model": MODEL_ID,
        "prompt": prompt,
        "max_tokens": OUTPUT_SEQ_LEN,
        "stream": True,
        "temperature": 0.0,
    }

    start = time.perf_counter()
    first_token_time = None
    token_times: list[float] = []
    output_tokens = 0
    success = True

    try:
        async with semaphore:
            async with session.post(TARGET_URL, json=payload) as resp:
                if resp.status != 200:
                    body = await resp.text()
                    print(f"Request failed ({resp.status}): {body[:200]}", file=sys.stderr)
                    end = time.perf_counter()
                    return RequestResult(
                        ttft_ms=0, e2e_latency_ms=(end - start) * 1000,
                        itl_ms=0, output_tokens=0, input_tokens=INPUT_SEQ_LEN,
                        duration_seconds=end - start, success=False,
                    )

                async for line in resp.content:
                    decoded = line.decode("utf-8").strip()
                    if not decoded.startswith("data: "):
                        continue
                    data_str = decoded[6:]
                    if data_str == "[DONE]":
                        break
                    try:
                        chunk = json.loads(data_str)
                    except json.JSONDecodeError:
                        continue

                    choices = chunk.get("choices", [])
                    if not choices:
                        continue
                    text = choices[0].get("text", "")
                    if not text:
                        continue

                    now = time.perf_counter()
                    if first_token_time is None:
                        first_token_time = now
                    token_times.append(now)
                    output_tokens += 1

    except Exception as e:
        print(f"Request exception: {e}", file=sys.stderr)
        success = False

    end = time.perf_counter()
    e2e_ms = (end - start) * 1000
    duration_s = end - start
    ttft_ms = (first_token_time - start) * 1000 if first_token_time else e2e_ms

    # Inter-token latency: average time between successive tokens.
    if len(token_times) > 1:
        itl_values = [
            (token_times[i] - token_times[i - 1]) * 1000
            for i in range(1, len(token_times))
        ]
        itl_ms = sum(itl_values) / len(itl_values)
    else:
        itl_ms = 0.0

    return RequestResult(
        ttft_ms=ttft_ms,
        e2e_latency_ms=e2e_ms,
        itl_ms=itl_ms,
        output_tokens=output_tokens,
        input_tokens=INPUT_SEQ_LEN,
        duration_seconds=duration_s,
        success=success,
    )


async def run_batch(session: aiohttp.ClientSession, n: int, label: str) -> list[RequestResult]:
    """Run n requests with bounded concurrency."""
    semaphore = asyncio.Semaphore(CONCURRENCY)
    tasks = [send_request(session, semaphore) for _ in range(n)]
    results = []
    for i, coro in enumerate(asyncio.as_completed(tasks)):
        result = await coro
        status = "ok" if result.success else "FAIL"
        print(f"[{label}] {i+1}/{n} {status} ttft={result.ttft_ms:.1f}ms e2e={result.e2e_latency_ms:.1f}ms tokens={result.output_tokens}", file=sys.stderr)
        results.append(result)
    return results


async def main():
    timeout = aiohttp.ClientTimeout(total=300)
    async with aiohttp.ClientSession(timeout=timeout) as session:
        # Warmup
        if WARMUP_REQUESTS > 0:
            print(f"Running {WARMUP_REQUESTS} warmup requests...", file=sys.stderr)
            await run_batch(session, WARMUP_REQUESTS, "warmup")
            print("Warmup complete.", file=sys.stderr)

        # Benchmark
        print(f"Running {NUM_REQUESTS} benchmark requests (concurrency={CONCURRENCY})...", file=sys.stderr)
        overall_start = time.perf_counter()
        results = await run_batch(session, NUM_REQUESTS, "bench")
        overall_end = time.perf_counter()

    total_duration = overall_end - overall_start
    successful = [r for r in results if r.success]
    failed = [r for r in results if not r.success]

    total_output_tokens = sum(r.output_tokens for r in successful)
    aggregate_tps = total_output_tokens / total_duration if total_duration > 0 else 0
    rps = len(successful) / total_duration if total_duration > 0 else 0

    output = {
        "requests": [asdict(r) for r in results],
        "summary": {
            "total_duration_seconds": total_duration,
            "total_requests": len(results),
            "successful_requests": len(successful),
            "failed_requests": len(failed),
            "throughput_aggregate_tps": aggregate_tps,
            "requests_per_second": rps,
        },
    }

    # Print JSON to stdout with markers so the orchestrator can find it
    # reliably even when K8s interleaves stdout and stderr.
    sys.stdout.flush()
    sys.stderr.flush()
    print("ACCELBENCH_JSON_BEGIN")
    print(json.dumps(output))
    print("ACCELBENCH_JSON_END")
    sys.stdout.flush()

    print(f"\n--- Summary ---", file=sys.stderr)
    print(f"Total: {len(results)} requests in {total_duration:.1f}s", file=sys.stderr)
    print(f"Success: {len(successful)}, Failed: {len(failed)}", file=sys.stderr)
    print(f"Aggregate throughput: {aggregate_tps:.1f} tokens/s", file=sys.stderr)
    print(f"Requests/sec: {rps:.2f}", file=sys.stderr)


if __name__ == "__main__":
    PROMPT_POOL = build_prompts()
    total_needed = NUM_REQUESTS + WARMUP_REQUESTS
    print(f"Prompt pool: {len(PROMPT_POOL)} prompts for {total_needed} requests", file=sys.stderr)
    asyncio.run(main())
