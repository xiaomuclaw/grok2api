#!/usr/bin/env python3
import csv
import json
import math
import os
import sqlite3
import struct
import time
import urllib.error
import urllib.parse
import urllib.request
from pathlib import Path


BASE_URL = os.environ.get("GROK_TEST_BASE_URL", "http://127.0.0.1:8000/v1").rstrip("/")
API_KEY = os.environ["GROK_TEST_API_KEY"]
SOURCE_URL = os.environ["GROK_TEST_SOURCE_URL"]
DB_PATH = os.environ.get("GROK_TEST_DB_PATH", "/var/lib/docker/volumes/grok2api_grok2api-data/_data/backend.db")
MEDIA_ROOT = Path(os.environ.get("GROK_TEST_MEDIA_ROOT", "/var/lib/docker/volumes/grok2api_grok2api-data/_data/media"))
OUTPUT_DIR = Path(os.environ.get("GROK_TEST_OUTPUT_DIR", "/opt/grok2api/test-results/web-image-edit-20260819"))
RATIOS = ["auto", "1:1", "16:9", "9:16", "4:3", "3:4", "3:2", "2:3"]
RESOLUTIONS = ["1k", "2k", "4k"]


def api_request(method, path, body=None, timeout=300):
    request = urllib.request.Request(
        BASE_URL + path,
        data=None if body is None else json.dumps(body, separators=(",", ":")).encode(),
        method=method,
        headers={"Authorization": "Bearer " + API_KEY, "Content-Type": "application/json"},
    )
    started = time.monotonic()
    try:
        with urllib.request.urlopen(request, timeout=timeout) as response:
            raw = response.read()
            return response.status, json.loads(raw) if raw else {}, round(time.monotonic() - started, 3)
    except urllib.error.HTTPError as error:
        raw = error.read()
        try:
            payload = json.loads(raw) if raw else {}
        except json.JSONDecodeError:
            payload = {"raw": raw.decode("utf-8", "replace")[:500]}
        return error.code, payload, round(time.monotonic() - started, 3)
    except Exception as error:
        return 0, {"error": {"message": str(error), "type": type(error).__name__}}, round(time.monotonic() - started, 3)


def error_fields(payload):
    error = payload.get("error", {}) if isinstance(payload, dict) else {}
    if not isinstance(error, dict):
        return {"error_message": str(error)[:500]}
    return {
        "error_code": str(error.get("code", "")),
        "error_type": str(error.get("type", "")),
        "error_message": str(error.get("message", ""))[:500],
    }


def asset_record(asset_id):
    database = sqlite3.connect(f"file:{DB_PATH}?mode=ro", uri=True)
    row = database.execute(
        "select storage_key,mime_type,size_bytes from media_assets where id=?", (asset_id,)
    ).fetchone()
    database.close()
    if not row:
        raise RuntimeError(f"asset metadata not found: {asset_id}")
    return row


def jpeg_dimensions(path):
    with path.open("rb") as stream:
        if stream.read(2) != b"\xff\xd8":
            raise ValueError("not a JPEG")
        while True:
            byte = stream.read(1)
            if not byte:
                break
            if byte != b"\xff":
                continue
            marker = stream.read(1)
            while marker == b"\xff":
                marker = stream.read(1)
            if marker in (b"\xd8", b"\xd9"):
                continue
            length_raw = stream.read(2)
            if len(length_raw) != 2:
                break
            length = struct.unpack(">H", length_raw)[0]
            if marker[0] in {0xC0, 0xC1, 0xC2, 0xC3, 0xC5, 0xC6, 0xC7, 0xC9, 0xCA, 0xCB, 0xCD, 0xCE, 0xCF}:
                data = stream.read(length - 2)
                height, width = struct.unpack(">HH", data[1:5])
                return width, height
            stream.seek(length - 2, 1)
    raise ValueError("JPEG dimensions not found")


def ratio_label(width, height):
    divisor = math.gcd(width, height)
    return f"{width // divisor}:{height // divisor}"


def run_edit(ratio, resolution, omitted=False):
    body = {
        "model": "grok-imagine-image-edit",
        "prompt": "Change the red cube to a vivid cobalt blue cube. Keep the plain white studio background and the same clean product-photo composition. Do not add text.",
        "image": {"url": SOURCE_URL},
        "n": 1,
        "resolution": resolution,
        "response_format": "url",
        "stream": False,
    }
    if not omitted:
        body["aspect_ratio"] = ratio
    final = None
    for attempt in range(1, 6):
        status, payload, elapsed = api_request("POST", "/images/edits", body)
        final = {
            "model": body["model"],
            "source_url": SOURCE_URL,
            "requested_ratio": "<omitted>" if omitted else ratio,
            "requested_resolution": resolution,
            "http_status": status,
            "request_seconds": elapsed,
            "attempt": attempt,
            "success": False,
        }
        data = payload.get("data", []) if isinstance(payload, dict) else []
        if status == 200 and data and isinstance(data[0], dict) and data[0].get("url"):
            asset_url = data[0]["url"]
            asset_id = urllib.parse.urlparse(asset_url).path.rstrip("/").split("/")[-1]
            storage_key, mime_type, size_bytes = asset_record(asset_id)
            width, height = jpeg_dimensions(MEDIA_ROOT / storage_key)
            final.update({
                "success": True,
                "asset_url": asset_url,
                "asset_id": asset_id,
                "mime_type": mime_type,
                "size_bytes": size_bytes,
                "actual_width": width,
                "actual_height": height,
                "actual_ratio_reduced": ratio_label(width, height),
                "actual_ratio_decimal": round(width / height, 6),
                "actual_max_edge": max(width, height),
            })
            break
        final.update(error_fields(payload))
        if status not in {0, 502, 503}:
            break
        time.sleep(4 * attempt)
    print("EDIT", final["requested_ratio"], resolution, final["http_status"], final.get("actual_width"), final.get("actual_height"), final.get("error_code"), flush=True)
    return final


def boundary_case(name, path, body):
    status, payload, elapsed = api_request("POST", path, body)
    record = {"name": name, "path": path, "http_status": status, "request_seconds": elapsed}
    record.update(error_fields(payload))
    print("BOUNDARY", name, status, record.get("error_code"), flush=True)
    return record


def write_csv(path, records):
    fields = sorted({field for record in records for field in record})
    with path.open("w", newline="", encoding="utf-8-sig") as stream:
        writer = csv.DictWriter(stream, fieldnames=fields)
        writer.writeheader()
        writer.writerows(records)


def main():
    OUTPUT_DIR.mkdir(parents=True, exist_ok=True)
    edits = [run_edit(ratio, resolution) for ratio in RATIOS for resolution in RESOLUTIONS]
    edits.append(run_edit("", "1k", omitted=True))
    boundaries = [
        boundary_case(
            "edit_model_on_generation_endpoint",
            "/images/generations",
            {"model": "grok-imagine-image-edit", "prompt": "Create a blue cube"},
        ),
        boundary_case(
            "generation_model_on_edit_endpoint",
            "/images/edits",
            {"model": "grok-imagine-image-2.0", "prompt": "Make it blue", "image": {"url": SOURCE_URL}},
        ),
        boundary_case(
            "edit_without_source_image",
            "/images/edits",
            {"model": "grok-imagine-image-edit", "prompt": "Make it blue"},
        ),
        boundary_case(
            "edit_requests_two_outputs",
            "/images/edits",
            {"model": "grok-imagine-image-edit", "prompt": "Make it blue", "image": {"url": SOURCE_URL}, "n": 2},
        ),
        boundary_case(
            "edit_with_quality_parameter",
            "/images/edits",
            {"model": "grok-imagine-image-edit", "prompt": "Make it blue", "image": {"url": SOURCE_URL}, "quality": "medium"},
        ),
    ]
    payload = {
        "tested_at_utc": time.strftime("%Y-%m-%dT%H:%M:%SZ", time.gmtime()),
        "source_url": SOURCE_URL,
        "edit_cases": edits,
        "boundary_cases": boundaries,
    }
    (OUTPUT_DIR / "results.json").write_text(json.dumps(payload, ensure_ascii=False, indent=2), encoding="utf-8")
    write_csv(OUTPUT_DIR / "edits.csv", edits)
    write_csv(OUTPUT_DIR / "boundaries.csv", boundaries)
    print("SUMMARY", json.dumps({"total": len(edits), "success": sum(item["success"] for item in edits)}), flush=True)


if __name__ == "__main__":
    main()
