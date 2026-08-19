# Grok Web media API integration test

Test date: 2026-08-19 (Asia/Shanghai)

Target: deployed Grok2API v3.1.3-upscale.1, with a Web-only client key.

The test read the dimensions and format from the stored output files. An HTTP 200 response alone was not counted as a successful media result.

## Executive summary

- All 75 image cases eventually produced a valid JPEG file.
- `grok-imagine-image` and `grok-imagine-image-2.0` honor every explicit aspect ratio tested.
- On Grok Web, `resolution=1k` and `resolution=2k` currently produce the same native dimensions. Do not advertise native 2K output.
- `resolution=4k` is the custom Grok2API delivery behavior: it generates at the Web native size and then resizes locally so the long edge is 4096 pixels.
- `grok-imagine-image-lite` accepts aspect ratio parameters but does not reliably honor them. It returned only `1168x784` or `784x1168` before 4K resizing.
- Web video works at `720p` for the Basic account pool. The stored result is MP4/H.264 (`avc1`) and a requested 6-second video measured 6.042 seconds.
- `480p` is not schedulable with the current Basic Web account pool.
- `1080p` is not supported by Web `grok-imagine-video`.
- Video does not accept `aspect_ratio=auto`. Omitting `aspect_ratio` defaults to `16:9`; omitting `resolution` defaults to `720p`.

## Image models

Endpoint: `POST /v1/images/generations`

### grok-imagine-image and grok-imagine-image-2.0

Both models returned the same dimension mapping in this test.

| Requested ratio | Actual 1K | Actual 2K | Actual 4K |
|---|---:|---:|---:|
| `auto` | 768x1152 | 768x1152 | 2731x4096 |
| `1:1` | 960x960 | 960x960 | 4096x4096 |
| `16:9` | 1280x720 | 1280x720 | 4096x2304 |
| `9:16` | 720x1280 | 720x1280 | 2304x4096 |
| `4:3` | 1088x816 | 1088x816 | 4096x3072 |
| `3:4` | 816x1088 | 816x1088 | 3072x4096 |
| `3:2` | 1152x768 | 1152x768 | 4096x2731 |
| `2:3` | 768x1152 | 768x1152 | 2731x4096 |

The `auto` result is content-dependent. With the fixed test prompt it selected portrait `2:3`; this must not be treated as a guaranteed auto output ratio.

Omitting `aspect_ratio` also worked and selected `768x1152` for both models in this run.

All returned files were JPEG. The 4K dimensions with `2731` use integer rounding while preserving a 4096-pixel long edge.

### grok-imagine-image-lite

The Lite model did not reliably honor the requested ratio.

| Requested ratio | Observed native outputs | Observed 4K outputs | Reliable? |
|---|---|---|---|
| `auto` | 1168x784 | 4096x2749 | Auto orientation only |
| `1:1` | 1168x784, 784x1168 | 4096x2749 | No |
| `16:9` | 784x1168, 1168x784 | 4096x2749 | No |
| `9:16` | 1168x784 | 4096x2749 | No |
| `4:3` | 784x1168 | 2749x4096 | No |
| `3:4` | 1168x784 | 2749x4096 | No |
| `3:2` | 784x1168, 1168x784 | 2749x4096 | No |
| `2:3` | 784x1168 | 4096x2749 | No |

`1k` and `2k` again had no meaningful size distinction. The 4K path only enlarged the native Lite output; it did not correct the requested ratio.

Integration recommendation: use Lite only when speed matters and the exact canvas ratio does not matter. Use `grok-imagine-image` or `grok-imagine-image-2.0` when downstream layout depends on dimensions.

## Video model

Endpoint: `POST /v1/videos/generations`

Model: `grok-imagine-video`

### Resolution support

| Requested resolution | Current Web Basic pool | Result |
|---|---|---|
| `480p` | Not schedulable | HTTP 503 `upstream_model_unavailable` for all seven ratios |
| `720p` | Supported | Valid MP4 files generated |
| `1080p` | Unsupported by this model | HTTP 400 `unsupported_model` for all seven ratios |
| omitted | Defaults to `720p` | Request default confirmed by gateway behavior; final media was blocked after the pool quota reached zero |

### 720p output metadata

| Requested ratio | Actual pixels | Duration | Container / codec | File size |
|---|---:|---:|---|---:|
| `16:9` | 1280x720 | 6.042 s | MP4 / H.264 `avc1` | 1,568,664 bytes |
| `9:16` | 720x1280 | 6.042 s | MP4 / H.264 `avc1` | 1,524,316 bytes |
| `4:3` | 1104x832 | 6.042 s | MP4 / H.264 `avc1` | 1,333,563 bytes |
| `3:4` | 832x1104 | 6.042 s | MP4 / H.264 `avc1` | 1,154,253 bytes |
| `3:2` | 1168x784 | 6.042 s | MP4 / H.264 `avc1` | 1,343,471 bytes |
| `1:1` | No final file | - | Request was accepted, but all attempts used the old failing proxy before it was replaced | - |
| `2:3` | No final file | - | Pool quota reached zero before this case ran | - |

The non-16-pixel-exact ratios are encoder-aligned approximations:

- `1104x832` is approximately 4:3.
- `832x1104` is approximately 3:4.
- `1168x784` is approximately 3:2.

Do not require mathematically exact ratio equality in a consumer. Compare with a tolerance or use the actual returned media dimensions.

The current 720p quota was exhausted during this matrix. Database state showed all 487 Web account quota rows at zero after the test, so the missing `1:1`, `2:3`, and omitted-default final files cannot be retried until quota recovery.

## Recommended request bodies

Exact-ratio native image:

```json
{
  "model": "grok-imagine-image-2.0",
  "prompt": "A product photograph on a white background",
  "n": 1,
  "aspect_ratio": "16:9",
  "resolution": "1k",
  "response_format": "url",
  "stream": false
}
```

Custom delivered 4K image:

```json
{
  "model": "grok-imagine-image-2.0",
  "prompt": "A product photograph on a white background",
  "n": 1,
  "aspect_ratio": "16:9",
  "resolution": "4k",
  "response_format": "url",
  "stream": false
}
```

720p Web video:

```json
{
  "model": "grok-imagine-video",
  "prompt": "A red paper airplane glides over a white desk",
  "duration": 6,
  "aspect_ratio": "16:9",
  "resolution": "720p"
}
```

Video creation is asynchronous. Save `request_id`, poll `GET /v1/videos/{request_id}`, and treat only `status=done` plus a readable video URL as success.

## Consumer rules

1. For exact image ratios, use `grok-imagine-image` or `grok-imagine-image-2.0`.
2. Treat Web `1k` and `2k` as the same native output tier until the upstream behavior changes.
3. Treat `4k` as Grok2API local post-processing, not native Grok Web generation.
4. Do not use Lite when an exact output canvas is required.
5. For Web Basic video, send `resolution=720p` explicitly.
6. Do not send `aspect_ratio=auto` for video; omit the field to get the `16:9` default.
7. Always inspect actual output dimensions instead of deriving them solely from the request.
8. Handle HTTP 429 quota recovery, HTTP 502 transient upstream/proxy failures, and asynchronous video failure as retryable operational states.

## Raw data

- `results.json`: complete structured records, including request timing, errors, asset IDs, file sizes, dimensions, duration, and codec.
- `images.csv`: all 75 final image cases.
- `videos.csv`: all 22 video resolution/ratio cases.

The dedicated test client key was disabled again after testing.
