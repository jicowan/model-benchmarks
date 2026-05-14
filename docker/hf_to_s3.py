#!/usr/bin/env python3
"""
Stream a HuggingFace model's files directly into S3 without buffering the
whole model (or even a single file) to local disk.

Each file is fetched via an HTTP stream from HuggingFace and piped through
boto3's multipart upload to S3. Only one multipart chunk (~8 MiB) is held
in memory at a time.

Usage:
    hf_to_s3.py MODEL_ID --revision REVISION \\
                --bucket BUCKET --prefix models/org/name
"""

import argparse
import os
import sys
from urllib.parse import urljoin, urlparse

import boto3
from boto3.s3.transfer import TransferConfig
from huggingface_hub import HfApi, hf_hub_url
from huggingface_hub.utils import build_hf_headers
import requests

_ALLOWED_HF_HOSTS = ("huggingface.co", "cdn-lfs.huggingface.co", "cdn-lfs.hf.co", "hf.co")


def _assert_hf_host(url: str) -> None:
    p = urlparse(url)
    if p.scheme != "https" or p.hostname not in _ALLOWED_HF_HOSTS:
        raise ValueError(f"refusing to fetch non-HuggingFace URL: {url}")


def _stream_from_hf(url: str, headers):
    """GET with manual redirect handling; every hop must stay on huggingface.co.

    HF's CDN returns relative Location headers (e.g. /api/resolve-cache/...);
    resolve them against the current URL before the host check. A relative
    redirect cannot change host by definition, so it's safe.
    """
    for _ in range(5):
        _assert_hf_host(url)
        r = requests.get(url, headers=headers, stream=True, timeout=300, allow_redirects=False)
        if r.is_redirect or r.is_permanent_redirect:
            location = r.headers.get("Location", "")
            r.close()
            url = urljoin(url, location)
            continue
        return r
    raise RuntimeError("too many redirects")


def main():
    p = argparse.ArgumentParser()
    p.add_argument("model_id")
    p.add_argument("--revision", default="main")
    p.add_argument("--bucket", required=True)
    p.add_argument("--prefix", required=True,
                   help="Key prefix under the bucket, e.g. models/meta-llama/Llama-3.1-8B")
    args = p.parse_args()

    token = os.environ.get("HF_TOKEN") or None
    headers = build_hf_headers(token=token)

    api = HfApi(token=token)
    info = api.model_info(
        args.model_id, revision=args.revision, files_metadata=False
    )
    files = [s.rfilename for s in info.siblings]

    # Use Pod Identity / instance creds via the default chain.
    s3 = boto3.client("s3")

    # 8 MiB parts, up to 10 concurrent; plenty for single-file streaming.
    cfg = TransferConfig(
        multipart_chunksize=8 * 1024 * 1024,
        multipart_threshold=8 * 1024 * 1024,
        max_concurrency=10,
        use_threads=True,
    )

    prefix = args.prefix.rstrip("/")
    total_bytes = 0

    for i, rfilename in enumerate(files, 1):
        key = f"{prefix}/{rfilename}"
        url = hf_hub_url(
            repo_id=args.model_id, filename=rfilename, revision=args.revision
        )
        print(f"[{i}/{len(files)}] {rfilename} -> s3://{args.bucket}/{key}", flush=True)

        with _stream_from_hf(url, headers) as r:
            r.raise_for_status()
            length = int(r.headers.get("Content-Length") or 0)

            # boto3's upload_fileobj reads from the file-like object. The
            # HTTP response's `.raw` is a file-like stream over the body.
            # Enable decode_content so requests doesn't silently leave
            # gzip/etc encoded data in the stream.
            r.raw.decode_content = True
            s3.upload_fileobj(
                Fileobj=r.raw,
                Bucket=args.bucket,
                Key=key,
                Config=cfg,
            )

            if length:
                total_bytes += length
            else:
                head = s3.head_object(Bucket=args.bucket, Key=key)
                total_bytes += head["ContentLength"]

    print(f"CACHE_COMPLETE size_bytes={total_bytes}")


if __name__ == "__main__":
    try:
        main()
    except Exception as e:
        print(f"ERROR: {e}", file=sys.stderr)
        sys.exit(1)
