# Grok Web 模型对接与实测指南

测试日期：2026-08-19（Asia/Shanghai）<br>
测试环境：Grok2API `v3.1.3-upscale.2`<br>
测试方式：所有媒体结果均下载并读取文件元数据；HTTP 200 但没有有效文件不计为成功。

## 1. 对接结论

| 场景 | 推荐模型 | 当前可用规格 | 关键限制 |
|---|---|---|---|
| 文字聊天 | `grok-chat-fast` / `auto` / `expert` / `heavy` | SSE 或非流式 | 只用于文字与多模态聊天，不用于图片编辑 |
| 精确比例文生图 | `grok-imagine-image-2.0` | 1K、2K、二改 4K | Web 的 1K 与 2K 实际像素相同 |
| 兼容文生图 | `grok-imagine-image` | 1K、2K、二改 4K | 尺寸表现与 2.0 相同 |
| 快速文生图 | `grok-imagine-image-lite` | 1K、2K、二改 4K | 不可靠遵守指定比例，不适合固定画布 |
| 图片编辑/图生图 | `grok-imagine-image-edit` | 1K、二改 4K | 必须传 1-8 张原图，`n` 只能为 1；2K 当前拒绝 |
| 视频生成 | `grok-imagine-video` | 720p，1-15 秒 | 当前 Basic Web 池不能调度 480p；Web 不支持 1080p |

最重要的消费端规则：

1. 不能只根据请求参数推算尺寸，必须读取最终图片或视频的实际宽高。
2. 文生图的 `1k` 与 `2k` 当前没有真实像素差异，不能向用户宣传为原生 2K。
3. `4k` 是本项目二改的本地后处理：先生成 Web 原生图，再等比放大到长边 4096，不是 Grok 原生 4K。
4. 精确比例不要使用 Lite；图片编辑应走 `/v1/images/edits`，不能放到文生图接口。
5. 视频只建议显式请求 `720p`；创建成功后保存 `request_id` 并轮询，不要重复提交同一任务。

## 2. 模型与接口

### 2.1 文字聊天

接口：`POST /v1/chat/completions` 或 `POST /v1/responses`

当前 Grok Web 文字路由包括：

- `grok-chat-auto`：由 Grok 自动选择处理模式。
- `grok-chat-fast`：低延迟常规聊天。
- `grok-chat-expert`：偏复杂任务。
- `grok-chat-heavy`：更重的推理任务，通常耗时更长。

部署中的路由表可能显示 `Web/` 前缀；是否允许无前缀别名由 Client Key 的 `allowModelAliases` 决定。跨项目对接前应调用 `GET /v1/models`，以该 Key 实际返回的模型 ID 为准。

示例：

```json
{
  "model": "grok-chat-fast",
  "messages": [
    {"role": "user", "content": "用一句话说明这个产品的卖点"}
  ],
  "stream": false
}
```

### 2.2 文生图

接口：`POST /v1/images/generations`

支持模型：

- `grok-imagine-image`
- `grok-imagine-image-2.0`
- `grok-imagine-image-lite`

标准请求：

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

## 3. 文生图真实尺寸

### 3.1 `grok-imagine-image` 与 `grok-imagine-image-2.0`

两个模型在本轮测试中的尺寸映射一致，75 个文生图用例均生成了有效 JPEG。

| 请求比例 | 实际 1K | 实际 2K | 实际二改 4K |
|---|---:|---:|---:|
| `auto` | 768×1152 | 768×1152 | 2731×4096 |
| `1:1` | 960×960 | 960×960 | 4096×4096 |
| `16:9` | 1280×720 | 1280×720 | 4096×2304 |
| `9:16` | 720×1280 | 720×1280 | 2304×4096 |
| `4:3` | 1088×816 | 1088×816 | 4096×3072 |
| `3:4` | 816×1088 | 816×1088 | 3072×4096 |
| `3:2` | 1152×768 | 1152×768 | 4096×2731 |
| `2:3` | 768×1152 | 768×1152 | 2731×4096 |

`auto` 与省略 `aspect_ratio` 都能调用，但方向由内容决定。本次固定提示词选择了 2:3 竖图，不能把它当作固定规则。

### 3.2 `grok-imagine-image-lite`

Lite 接受比例参数，但实际只稳定观察到两种原生画布：`1168×784` 和 `784×1168`。请求比例与返回方向有时相反，4K 也只是把这两种原生画布放大到 `4096×2749` 或 `2749×4096`。

因此 Lite 只适合“速度优先、后续可以裁切”的场景。电商主图、海报、视频封面等依赖固定画布的业务不要使用 Lite。

## 4. 图片编辑/图生图

模型：`grok-imagine-image-edit`<br>
接口：`POST /v1/images/edits`

这个模型不是文生图模型。它会先上传原图，再结合提示词生成编辑后的新图片。

### 4.1 参数限制

- `image` 必填，可传 1-8 张。
- `n` 目前只能为 1。
- 原生编辑只接受 `resolution=1k`。
- 本项目二改支持 `resolution=4k`，其含义仍是生成后本地等比放大。
- `resolution=2k` 当前返回 HTTP 400，不要发送。
- 把编辑模型传给 `/v1/images/generations` 会返回模型/接口不匹配错误。
- 本轮所有有效输出均为 JPEG。

### 4.2 实测尺寸

测试原图为 960×960，提示词要求改变主体颜色并保持背景和构图。所有 8 个 1K 比例、所有 8 个二改 4K 比例以及省略比例的 1K 用例最终均成功。

| 请求比例 | 实际 1K | 实际二改 4K | 2K |
|---|---:|---:|---|
| `auto` | 1024×1024 | 4096×4096 | HTTP 400 |
| `1:1` | 1024×1024 | 4096×4096 | HTTP 400 |
| `16:9` | 1280×720 | 4096×2304 | HTTP 400 |
| `9:16` | 720×1280 | 2304×4096 | HTTP 400 |
| `4:3` | 1152×864 | 4096×3072 | HTTP 400 |
| `3:4` | 864×1152 | 3072×4096 | HTTP 400 |
| `3:2` | 1248×832 | 4096×2731 | HTTP 400 |
| `2:3` | 832×1248 | 2731×4096 | HTTP 400 |
| 省略 | 1024×1024 | 未单独测试 | - |

编辑模型与文生图模型的原生尺寸并不完全相同，例如编辑的 1:1 为 1024×1024，而标准文生图为 960×960。下游不能共用一张静态尺寸表。

请求示例：

```json
{
  "model": "grok-imagine-image-edit",
  "prompt": "Change the red cube to cobalt blue and keep the white background",
  "image": {
    "url": "https://example.com/source.jpg"
  },
  "n": 1,
  "aspect_ratio": "1:1",
  "resolution": "1k",
  "response_format": "url",
  "stream": false
}
```

## 5. 视频生成

模型：`grok-imagine-video`<br>
创建：`POST /v1/videos/generations`<br>
查询：`GET /v1/videos/{request_id}`

### 5.1 清晰度支持

| 请求清晰度 | 当前 Grok Web Basic 账号池 | 对接结论 |
|---|---|---|
| `480p` | HTTP 503，当前路由不可调度 | 不要用于生产 |
| `720p` | 支持 | 生产环境显式指定 |
| `1080p` | HTTP 400，Web 模型不支持 | 不要发送 |
| 省略 | 默认为 720p | 仍建议显式填写 |

### 5.2 720p 实际尺寸

| 请求比例 | 实际像素 | 时长 | 容器/编码 |
|---|---:|---:|---|
| `16:9` | 1280×720 | 6.042 秒 | MP4 / H.264 `avc1` |
| `9:16` | 720×1280 | 6.042 秒 | MP4 / H.264 `avc1` |
| `4:3` | 1104×832 | 6.042 秒 | MP4 / H.264 `avc1` |
| `3:4` | 832×1104 | 6.042 秒 | MP4 / H.264 `avc1` |
| `3:2` | 1168×784 | 6.042 秒 | MP4 / H.264 `avc1` |
| `1:1` | 960×960 | 6.042 秒 | MP4 / H.264 `avc1` |
| `2:3` | 784×1168 | 6.042 秒 | MP4 / H.264 `avc1` |

编码器会按像素块对齐，`4:3`、`3:4`、`3:2`、`2:3` 不一定数学上完全相等。消费端应允许小幅误差，或直接使用文件真实宽高。视频不接受 `aspect_ratio=auto`；省略比例时默认 16:9。

创建示例：

```json
{
  "model": "grok-imagine-video",
  "prompt": "A red paper airplane glides over a white desk",
  "duration": 6,
  "aspect_ratio": "16:9",
  "resolution": "720p"
}
```

推荐轮询流程：

1. 创建接口返回 200 后立即保存 `request_id`。
2. 每 5-10 秒查询一次 `/v1/videos/{request_id}`。
3. `status=pending` 或处理中状态继续等待。
4. 只有 `status=done` 且视频 URL 可读取才算成功。
5. `status=failed` 时记录错误，不要继续轮询同一个 ID。
6. 已拿到 `request_id` 后不要重新创建任务，否则可能重复计费并生成重复视频。

## 6. 错误与重试策略

| HTTP/状态 | 含义 | 下游处理 |
|---|---|---|
| 400 | 参数、模型或接口不支持 | 修正请求，不重试原参数 |
| 401 | Client Key 或上游账号认证异常 | 停止快速重试，检查凭据/账号状态 |
| 403 code 7 | Grok Statsig 页面签名过期 | `upscale.2` 已在网关内部失效签名并重签一次 |
| 429 | 配额或速率限制 | 指数退避，并切换有额度的账号/Key |
| 502 | 上游连接、代理 EOF 或媒体传输失败 | 最多重试 2 次，使用 2-5 秒抖动退避 |
| 503 | 当前没有可调度账号、规格或出口 | 检查模型规格、额度、出口健康；不要无间隔循环 |
| 视频 `failed` | 异步任务最终失败 | 记录任务 ID 和错误；确认可重试后创建新任务 |

同步图片请求重试可能在上游已受理但响应丢失时产生重复结果。对业务订单应使用自己的请求记录和去重键，不能把“客户端超时”直接等同于“上游未执行”。

## 7. 代理与稳定性测试

### 7.1 测试过程

ProxyHub 中先筛选了 9 个 US/JP/KR 住宅 OpenVPN 节点，每个节点连续 5 次访问 Grok 均成功，出口 IP 也保持不变。但基础连通成功不能代表图片上传、WebSocket 或视频长任务稳定，因此又对 5 个候选出口进行了真实模型调用。

| 候选 | 文字 | 标准文生图 | 图片编辑/长媒体 | 结论 |
|---|---|---|---|---|
| US-A | 成功 | WebSocket 有波动 | 不稳定 | 删除 |
| US-B | 成功 | 成功 | 单节点出现波动 | 删除临时出口 |
| US-C | 成功 | 成功 | 编辑矩阵与 3 条视频成功 | 选为正式主节点 |
| JP-A | 成功 | 失败 | 不适合媒体 | 删除 |
| KR-A | 成功 | 失败 | 不适合媒体 | 删除 |

### 7.2 最终配置

- ProxyHub 正式出口：`grok`，内部端口 19501。
- 模式：`pin`。
- 主出口：US-C 美国住宅 OpenVPN 节点。
- 备用：主节点不可用时，仅在同一筛选条件的美国住宅 OpenVPN 节点中自动回退。
- Grok2API 出口节点：`proxyhub-grok_web-us-fixed-failover`，`proxyPool=true`。
- 最终探测：healthy，固定出口 IP 与预期一致。
- 正式端口复验：文字聊天 HTTP 200；图片编辑 HTTP 200；同一主节点的视频 1:1、2:3、16:9 全部完成。
- 本轮临时端口 19502-19506 已从 ProxyHub 数据库和配置删除，并停止监听。

选择固定出口是为了让 Grok SSO、Cloudflare Cookie、Statsig 和长连接保持同一网络身份。每次请求随机换国家/出口的 rotate 模式不适合 Grok Web 媒体任务。

### 7.3 Statsig 修复

公共签名服务受 Cloudflare 保护时，旧实现会继续使用过期签名，并收到：

```text
403 / code 7 / This page is out of date. Reload to continue.
```

当前部署增加了仅位于 Docker 内网的签名中继：

- 通过现有 FlareSolverr 获取匹配的 `cf_clearance` 与 User-Agent。
- 只接受 `POST /sign`，限制请求体大小。
- 不记录 Cookie、完整 meta 或签名值。
- 签名服务 403 时刷新 Clearance 并重试一次。
- Grok 媒体接口返回明确的 code 7 时，网关使缓存签名失效并重新签名一次。
- 签名缓存由 1 小时缩短为 90 秒。

## 8. 质量守护

质量守护 Sidecar 已启用并运行在 `hybrid` 模式：主动探测间隔 30 分钟，被动审计轮询 5 秒，连续异常才隔离，隔离 5 分钟，并保持至少 3 个健康 Build 出口。`failClosed=false`，避免守护组件自身异常导致全部业务停服。

注意：当前质量守护针对 Grok Build 文字出口质量，不替代 Grok Web 图片/视频的真实媒体验收。媒体稳定性仍应监控图片成功落盘率、视频最终完成率、502/503 比例和任务耗时。

## 9. 验收状态

- 487 个 Grok Web 账号：全部 enabled、active，验收时无账号级 cooldown。
- 测试专用 Client Key：验收结束后已禁用，并恢复并发 2。
- Grok2API：`v3.1.3-upscale.2`，容器健康。
- Statsig 中继：容器运行，未暴露公网端口。
- ProxyHub：只保留通用出口和正式 `grok` 出口；5 个临时候选已实际删除。
- 原始结构化结果位于 `test-results/web-media-matrix-20260819/` 和 `test-results/web-image-edit-20260819/`。

本文档不包含服务器密码、管理密码、代理密码、Client Key、Cookie 或账号 Token。
