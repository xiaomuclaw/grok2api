#!/usr/bin/env python3
import concurrent.futures
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
DB_PATH = os.environ.get("GROK_TEST_DB_PATH", "/var/lib/docker/volumes/grok2api_grok2api-data/_data/backend.db")
MEDIA_ROOT = Path(os.environ.get("GROK_TEST_MEDIA_ROOT", "/var/lib/docker/volumes/grok2api_grok2api-data/_data/media"))
OUTPUT_DIR = Path(os.environ.get("GROK_TEST_OUTPUT_DIR", "/opt/grok2api/test-results/web-media-matrix-20260819"))

IMAGE_MODELS = [
    "grok-imagine-image-lite",
    "grok-imagine-image",
    "grok-imagine-image-2.0",
]
RATIOS = ["auto", "1:1", "16:9", "9:16", "4:3", "3:4", "3:2", "2:3"]
VIDEO_RATIOS = ["1:1", "16:9", "9:16", "4:3", "3:4", "3:2", "2:3"]
IMAGE_RESOLUTIONS = ["1k", "2k", "4k"]
VIDEO_RESOLUTIONS = ["480p", "720p", "1080p"]


def api_request(method, path, body=None, timeout=300):
    data = None if body is None else json.dumps(body, separators=(",", ":")).encode()
    request = urllib.request.Request(
        BASE_URL + path,
        data=data,
        method=method,
        headers={"Authorization": "Bearer " + API_KEY, "Content-Type": "application/json"},
    )
    started = time.monotonic()
    try:
        with urllib.request.urlopen(request, timeout=timeout) as response:
            raw = response.read()
            payload = json.loads(raw) if raw else {}
            return response.status, payload, round(time.monotonic() - started, 3)
    except urllib.error.HTTPError as error:
        raw = error.read()
        try:
            payload = json.loads(raw) if raw else {}
        except json.JSONDecodeError:
            payload = {"raw": raw.decode("utf-8", "replace")[:500]}
        return error.code, payload, round(time.monotonic() - started, 3)
    except Exception as error:
        return 0, {"error": {"message": str(error), "type": type(error).__name__}}, round(time.monotonic() - started, 3)


def error_summary(payload):
    error = payload.get("error", {}) if isinstance(payload, dict) else {}
    if isinstance(error, dict):
        return {
            "error_code": str(error.get("code", "")),
            "error_type": str(error.get("type", "")),
            "error_message": str(error.get("message", ""))[:500],
        }
    return {"error_code": "", "error_type": "", "error_message": str(error)[:500]}


def asset_record(asset_id):
    deadline = time.monotonic() + 15
    while True:
        database = sqlite3.connect(f"file:{DB_PATH}?mode=ro", uri=True)
        row = database.execute(
            "select storage_key,mime_type,size_bytes from media_assets where id=?", (asset_id,)
        ).fetchone()
        database.close()
        if row:
            return row
        if time.monotonic() >= deadline:
            raise RuntimeError(f"asset metadata not found: {asset_id}")
        time.sleep(0.25)


def jpeg_dimensions(path):
    with path.open("rb") as stream:
        if stream.read(2) != b"\xff\xd8":
            raise ValueError("not a JPEG")
        while True:
            marker_start = stream.read(1)
            if not marker_start:
                break
            if marker_start != b"\xff":
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
                return struct.unpack(">HH", data[1:5])[::-1]
            stream.seek(length - 2, 1)
    raise ValueError("JPEG dimensions not found")


def image_dimensions(path, mime_type):
    if mime_type == "image/jpeg" or path.suffix.lower() in {".jpg", ".jpeg"}:
        return jpeg_dimensions(path)
    with path.open("rb") as stream:
        head = stream.read(32)
    if head.startswith(b"\x89PNG\r\n\x1a\n"):
        return struct.unpack(">II", head[16:24])
    raise ValueError(f"unsupported image format: {mime_type}")


def ratio_label(width, height):
    divisor = math.gcd(width, height)
    return f"{width // divisor}:{height // divisor}"


def test_image(case):
    model, ratio, resolution, omitted = case
    body = {
        "model": model,
        "prompt": "Technical test image: a red cube centered on a plain white studio background, no text",
        "n": 1,
        "resolution": resolution,
        "response_format": "url",
        "stream": False,
    }
    if not omitted:
        body["aspect_ratio"] = ratio
    status, payload, elapsed = api_request("POST", "/images/generations", body)
    record = {
        "kind": "image",
        "model": model,
        "requested_ratio": "<omitted>" if omitted else ratio,
        "requested_resolution": resolution,
        "http_status": status,
        "request_seconds": elapsed,
        "success": False,
    }
    data = payload.get("data", []) if isinstance(payload, dict) else []
    if status == 200 and data and isinstance(data[0], dict) and data[0].get("url"):
        url = data[0]["url"]
        asset_id = urllib.parse.urlparse(url).path.rstrip("/").split("/")[-1]
        storage_key, mime_type, size_bytes = asset_record(asset_id)
        path = MEDIA_ROOT / storage_key
        width, height = image_dimensions(path, mime_type)
        record.update({
            "success": True,
            "asset_id": asset_id,
            "mime_type": mime_type,
            "size_bytes": size_bytes,
            "actual_width": width,
            "actual_height": height,
            "actual_ratio_reduced": ratio_label(width, height),
            "actual_ratio_decimal": round(width / height, 6),
            "actual_max_edge": max(width, height),
        })
    else:
        record.update(error_summary(payload))
    print("IMAGE", model, record["requested_ratio"], resolution, status, record.get("actual_width"), record.get("actual_height"), flush=True)
    return record


def media_job_record(job_id):
    database = sqlite3.connect(f"file:{DB_PATH}?mode=ro", uri=True)
    row = database.execute(
        "select status,progress,error_code,error_message,result_asset_id from media_jobs where id=?", (job_id,)
    ).fetchone()
    database.close()
    return row


def mp4_metadata(path):
    data = path.read_bytes()
    width = height = 0
    position = 0
    while True:
        position = data.find(b"tkhd", position)
        if position < 4:
            break
        start = position - 4
        size = struct.unpack(">I", data[start:position])[0]
        if size >= 16 and start + size <= len(data):
            candidate_width = struct.unpack(">I", data[start + size - 8:start + size - 4])[0] >> 16
            candidate_height = struct.unpack(">I", data[start + size - 4:start + size])[0] >> 16
            if candidate_width > 0 and candidate_height > 0 and candidate_width * candidate_height > width * height:
                width, height = candidate_width, candidate_height
        position += 4
    duration = None
    position = data.find(b"mvhd")
    if position >= 4:
        start = position - 4
        version = data[position + 4]
        if version == 0 and position + 24 <= len(data):
            timescale = struct.unpack(">I", data[position + 16:position + 20])[0]
            raw_duration = struct.unpack(">I", data[position + 20:position + 24])[0]
        elif version == 1 and position + 36 <= len(data):
            timescale = struct.unpack(">I", data[position + 28:position + 32])[0]
            raw_duration = struct.unpack(">Q", data[position + 32:position + 40])[0]
        else:
            timescale = raw_duration = 0
        if timescale:
            duration = round(raw_duration / timescale, 3)
    codec = ""
    for candidate in (b"avc1", b"hvc1", b"hev1", b"av01", b"vp09"):
        if candidate in data:
            codec = candidate.decode()
            break
    return width, height, duration, codec


def create_video(case):
    ratio, resolution, omitted = case
    body = {
        "model": "grok-imagine-video",
        "prompt": "Technical test video: a red paper airplane glides slowly over a plain white desk",
        "duration": 6,
    }
    if not omitted:
        body.update({"aspect_ratio": ratio, "resolution": resolution})
    status, payload, elapsed = api_request("POST", "/videos/generations", body)
    record = {
        "kind": "video",
        "model": "grok-imagine-video",
        "requested_ratio": "<omitted>" if omitted else ratio,
        "requested_resolution": "<omitted>" if omitted else resolution,
        "requested_duration": 6,
        "create_http_status": status,
        "create_seconds": elapsed,
        "success": False,
    }
    job_id = payload.get("request_id") if isinstance(payload, dict) else None
    if status == 200 and job_id:
        record["job_id"] = job_id
    else:
        record.update(error_summary(payload))
    print("VIDEO_CREATE", record["requested_ratio"], record["requested_resolution"], status, job_id, flush=True)
    return record


def finish_video(record):
    if not record.get("job_id"):
        return record
    job_id = record["job_id"]
    deadline = time.monotonic() + 1200
    last_status = ""
    while time.monotonic() < deadline:
        status, payload, _ = api_request("GET", "/videos/" + urllib.parse.quote(job_id), timeout=60)
        state = payload.get("status", "") if isinstance(payload, dict) else ""
        if state != last_status:
            print("VIDEO_POLL", job_id, state, payload.get("progress"), flush=True)
            last_status = state
        if state in {"done", "failed"}:
            break
        time.sleep(8)
    row = media_job_record(job_id)
    if not row:
        record.update({"error_code": "job_missing", "error_message": "media job row missing"})
        return record
    job_status, progress, error_code, error_message, asset_id = row
    record.update({"final_status": job_status, "final_progress": progress})
    if job_status == "completed" and asset_id:
        storage_key, mime_type, size_bytes = asset_record(asset_id)
        path = MEDIA_ROOT / storage_key
        width, height, duration, codec = mp4_metadata(path)
        record.update({
            "success": True,
            "asset_id": asset_id,
            "mime_type": mime_type,
            "size_bytes": size_bytes,
            "actual_width": width,
            "actual_height": height,
            "actual_ratio_reduced": ratio_label(width, height) if width and height else "",
            "actual_ratio_decimal": round(width / height, 6) if width and height else None,
            "actual_duration_seconds": duration,
            "codec": codec,
        })
    else:
        record.update({"error_code": error_code, "error_message": error_message[:500]})
    print("VIDEO_DONE", record["requested_ratio"], record["requested_resolution"], job_status, record.get("actual_width"), record.get("actual_height"), flush=True)
    return record


def write_outputs(image_records, video_records):
    OUTPUT_DIR.mkdir(parents=True, exist_ok=True)
    payload = {
        "tested_at_utc": time.strftime("%Y-%m-%dT%H:%M:%SZ", time.gmtime()),
        "base_url": BASE_URL,
        "image_cases": image_records,
        "video_cases": video_records,
    }
    (OUTPUT_DIR / "results.json").write_text(json.dumps(payload, ensure_ascii=False, indent=2), encoding="utf-8")
    for name, records in (("images.csv", image_records), ("videos.csv", video_records)):
        fields = sorted({key for record in records for key in record})
        with (OUTPUT_DIR / name).open("w", newline="", encoding="utf-8-sig") as stream:
            writer = csv.DictWriter(stream, fieldnames=fields)
            writer.writeheader()
            writer.writerows(records)


def main():
    image_cases = [
        (model, ratio, resolution, False)
        for model in IMAGE_MODELS
        for ratio in RATIOS
        for resolution in IMAGE_RESOLUTIONS
    ]
    image_cases.extend((model, "", "1k", True) for model in IMAGE_MODELS)
    with concurrent.futures.ThreadPoolExecutor(max_workers=4) as executor:
        image_records = list(executor.map(test_image, image_cases))

    video_cases = [
        (ratio, resolution, False)
        for ratio in VIDEO_RATIOS
        for resolution in VIDEO_RESOLUTIONS
    ]
    video_cases.append(("", "", True))
    with concurrent.futures.ThreadPoolExecutor(max_workers=4) as executor:
        video_records = list(executor.map(create_video, video_cases))
    with concurrent.futures.ThreadPoolExecutor(max_workers=8) as executor:
        video_records = list(executor.map(finish_video, video_records))

    write_outputs(image_records, video_records)
    print("SUMMARY", json.dumps({
        "images_total": len(image_records),
        "images_ok": sum(record["success"] for record in image_records),
        "videos_total": len(video_records),
        "videos_ok": sum(record["success"] for record in video_records),
        "output_dir": str(OUTPUT_DIR),
    }), flush=True)


if __name__ == "__main__":
    main()
