#!/usr/bin/env python3
import json
import time
from pathlib import Path

import grok_web_media_matrix as runner


RESULT_PATH = runner.OUTPUT_DIR / "results.json"


def retry_image(previous):
    if previous.get("success"):
        return previous
    omitted = previous["requested_ratio"] == "<omitted>"
    case = (
        previous["model"],
        "" if omitted else previous["requested_ratio"],
        previous["requested_resolution"],
        omitted,
    )
    final = previous
    for attempt in range(1, 4):
        final = runner.test_image(case)
        final["sequential_retry_attempt"] = attempt
        if final.get("success"):
            return final
        time.sleep(4 * attempt)
    return final


def retry_video(previous):
    if previous.get("success"):
        return previous
    resolution = previous["requested_resolution"]
    if resolution not in {"720p", "<omitted>"}:
        return previous
    omitted = previous["requested_ratio"] == "<omitted>"
    case = (
        "" if omitted else previous["requested_ratio"],
        "" if omitted else resolution,
        omitted,
    )
    final = previous
    for attempt in range(1, 4):
        final = runner.create_video(case)
        final["sequential_retry_attempt"] = attempt
        final = runner.finish_video(final)
        if final.get("success"):
            return final
        time.sleep(8 * attempt)
    return final


def main():
    payload = json.loads(RESULT_PATH.read_text(encoding="utf-8"))
    images = []
    for index, previous in enumerate(payload["image_cases"], 1):
        images.append(retry_image(previous))
        print(f"IMAGE_RETRY_PROGRESS {index}/{len(payload['image_cases'])}", flush=True)

    videos = []
    for index, previous in enumerate(payload["video_cases"], 1):
        videos.append(retry_video(previous))
        print(f"VIDEO_RETRY_PROGRESS {index}/{len(payload['video_cases'])}", flush=True)

    runner.write_outputs(images, videos)
    print("FINAL_SUMMARY", json.dumps({
        "images_total": len(images),
        "images_ok": sum(record.get("success", False) for record in images),
        "videos_total": len(videos),
        "videos_ok": sum(record.get("success", False) for record in videos),
        "results": str(RESULT_PATH),
    }), flush=True)


if __name__ == "__main__":
    main()
