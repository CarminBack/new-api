# Seedance 2.0 视频生成 API 对接文档

本文档面向通过中转服务调用 Seedance 2.0 视频生成模型的下游开发者。

更新时间：2026-07-24

## 1. 接入信息

请向服务商获取以下两项配置：

```text
中转地址：https://token.mewinyou.shop/v1
API Key：由服务商分配，例如 sk-xxxxxxxx
```

不同客户端对“中转地址”的要求可能不同：

| 使用场景 | 应填写的地址 |
| --- | --- |
| OpenAI 兼容客户端的 Base URL | `https://token.mewinyou.shop/v1` |
| 本文档中的原始 HTTP 示例 | `https://token.mewinyou.shop` |

请勿重复拼接 `/v1`。例如 Base URL 已经是 `https://token.mewinyou.shop/v1` 时，不要再配置成 `/v1/v1/videos`。

## 2. 鉴权方式

所有请求都必须通过 Bearer Token 鉴权：

```http
Authorization: Bearer <YOUR_API_KEY>
```

JSON 请求还需要：

```http
Content-Type: application/json
```

请勿将 API Key 放在 URL 查询参数、公开网页代码、日志或代码仓库中。

## 3. 接口一览

推荐使用 OpenAI 兼容的视频接口：

| 功能 | 方法 | 路径 |
| --- | --- | --- |
| 创建视频任务 | `POST` | `/v1/videos` |
| 查询任务状态 | `GET` | `/v1/videos/{task_id}` |
| 下载或播放视频 | `GET` | `/v1/videos/{task_id}/content` |

视频生成是异步任务。创建成功后先保存响应中的 `id`，再轮询任务状态；任务完成后通过 content 接口获取视频文件。

## 4. 支持的模型

### 4.1 普通线路 `c47`

按视频秒数计费，支持 4-15 秒。

```text
seedance-480p-fast-c47
seedance-480p-c47
seedance-720p-fast-c47
seedance-720p-c47
seedance-1080p-c47
seedance-4k-c47
```

### 4.2 专线线路 `c48`

按视频秒数计费，支持 4-15 秒。

```text
seedance-480p-fast-c48
seedance-480p-c48
seedance-720p-fast-c48
seedance-720p-c48
seedance-1080p-c48
seedance-4k-c48
```

### 4.3 按条线路 `c49`

按生成任务计费，支持 4-15 秒。

```text
seedance-720p-fast-c49
seedance-720p-c49
```

### 4.4 特价线路 `c50`

按生成任务计费，支持 5-15 秒。

```text
seedance-720p-fast-c50
seedance-720p-c50
```

模型名称已经包含清晰度和线路信息。请求中的 `resolution` 必须与模型名称保持一致。模型和线路可能随同步配置调整，客户端不要写死完整模型列表；以服务商当前公开列表和本文档为准。

## 5. 创建视频任务

```http
POST /v1/videos
```

### 5.1 请求参数

| 字段 | 类型 | 必填 | 说明 |
| --- | --- | --- | --- |
| `model` | string | 是 | 上述公开模型名称。不要提交内部上游模型名。 |
| `prompt` | string | 是 | 视频生成提示词。 |
| `duration` | integer | 是 | 视频时长，单位为秒；必须符合所选线路范围。 |
| `resolution` | string | 是 | `480p`、`720p`、`1080p` 或 `4K`，并与模型名匹配。 |
| `size` | string | 是 | 视频画幅，例如 `16:9`、`9:16` 或 `1:1`。 |
| `mode_type` | string | 是 | `text2video`、`image2video` 或受支持时的 `frames2video`。 |
| `images` | string[] | 条件必填 | 图生视频传至少 1 张；首尾帧视频必须传 2 张，顺序为首帧、尾帧。建议使用公网可访问的 HTTPS URL。 |
| `videos` | string[] | 否 | 参考视频 URL 列表，受所选线路数量限制。 |
| `audios` | string[] | 否 | 参考音频 URL 列表，受所选线路数量限制。 |
| `n` | integer | 否 | 生成数量。当前必须使用 `1`。 |

以上是统一公共字段，适配器会根据 `model` 自动转换为真实渠道格式。为兼容旧客户端，也接受 `metadata.resolution`、`metadata.mode_type`、`metadata.images`、`metadata.videos`、`metadata.audios` 和 `metadata.ratio`；新接入请优先使用顶层字段。

建议始终显式提交 `duration`、`resolution`、`size`、`mode_type` 和 `n`，不要依赖客户端或上游默认值。

### 5.2 模式兼容性

| 模型类型 | 文生视频 | 图生视频 | 首尾帧视频 |
| --- | --- | --- | --- |
| `c47/c48` 全部模型 | 支持 | 支持 | 支持 |
| `c49` 全部模型 | 支持 | 支持 | 支持 |
| `c50` 全部模型 | 支持 | 支持 | 不支持 |

### 5.3 画幅限制

`c47/c48` 全部模型支持：

```text
16:9
9:16
1:1
4:3
3:4
21:9
2:3
3:2
```

`c49/c50` 当前模型支持：

```text
16:9
9:16
1:1
```

## 6. 请求示例

以下示例中的 `<YOUR_API_KEY>` 替换为服务商分配的 API Key。

### 6.1 文生视频

```bash
curl --location --request POST 'https://token.mewinyou.shop/v1/videos' \
  --header 'Authorization: Bearer <YOUR_API_KEY>' \
  --header 'Content-Type: application/json' \
  --data-raw '{
    "model": "seedance-720p-fast-c47",
    "prompt": "海边日落，镜头缓慢向前推进，电影感，柔和光线",
    "duration": 5,
    "resolution": "720p",
    "size": "16:9",
    "mode_type": "text2video",
    "n": 1
  }'
```

### 6.2 单图生视频

```bash
curl --location --request POST 'https://token.mewinyou.shop/v1/videos' \
  --header 'Authorization: Bearer <YOUR_API_KEY>' \
  --header 'Content-Type: application/json' \
  --data-raw '{
    "model": "seedance-720p-fast-c48",
    "prompt": "人物缓慢转身看向镜头，衣服和头发自然摆动",
    "images": [
      "https://example.com/input.jpg"
    ],
    "duration": 5,
    "resolution": "720p",
    "size": "16:9",
    "mode_type": "image2video",
    "n": 1
  }'
```

### 6.3 首尾帧生视频

首尾帧模式请选择当前仍存在且明确支持 `frames2video` 的 c47、c48 或 c49 模型。

```bash
curl --location --request POST 'https://token.mewinyou.shop/v1/videos' \
  --header 'Authorization: Bearer <YOUR_API_KEY>' \
  --header 'Content-Type: application/json' \
  --data-raw '{
    "model": "seedance-720p-fast-c47",
    "prompt": "从白天平滑过渡到夜晚，保持场景结构一致",
    "images": [
      "https://example.com/first-frame.jpg",
      "https://example.com/last-frame.jpg"
    ],
    "duration": 5,
    "resolution": "720p",
    "size": "16:9",
    "mode_type": "frames2video",
    "n": 1
  }'
```

## 7. 创建成功响应

创建成功后会返回公开任务 ID。响应可能包含额外字段，调用方应至少保存 `id` 并读取 `status`。

```json
{
  "id": "task_xxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxx",
  "task_id": "task_xxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxx",
  "object": "video",
  "model": "seedance-720p-fast-c47",
  "status": "queued",
  "progress": 0,
  "created_at": 1780000000
}
```

兼容响应中 `id` 和 `task_id` 通常相同。后续查询和下载统一使用公开任务 ID，不要使用其他内部 ID。

## 8. 查询任务状态

```http
GET /v1/videos/{task_id}
```

示例：

```bash
curl --location --request GET \
  'https://token.mewinyou.shop/v1/videos/task_xxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxx' \
  --header 'Authorization: Bearer <YOUR_API_KEY>'
```

建议每 3-5 秒轮询一次，不要高频连续请求。

需要兼容的状态包括：

| 状态 | 含义 | 是否最终状态 |
| --- | --- | --- |
| `queued`、`pending` | 排队中 | 否 |
| `processing`、`in_progress` | 生成中 | 否 |
| `completed` | 生成成功 | 是 |
| `failed`、`cancelled` | 生成失败或取消 | 是 |

状态查询响应可能包含上游扩展字段。调用方不应依赖未在本文档中声明的字段。

## 9. 获取视频内容

任务状态变为 `completed` 后，通过以下接口获取视频：

```http
GET /v1/videos/{task_id}/content
```

下载示例：

```bash
curl --location \
  'https://token.mewinyou.shop/v1/videos/task_xxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxx/content' \
  --header 'Authorization: Bearer <YOUR_API_KEY>' \
  --output output.mp4
```

该接口返回视频二进制内容，通常为 `video/mp4`。它也支持标准 HTTP `Range` 请求，便于播放器分段加载或断点续传。

## 10. JavaScript 示例

```javascript
const apiRoot = 'https://token.mewinyou.shop';
const apiKey = process.env.VIDEO_API_KEY;

async function createVideo() {
  const response = await fetch(`${apiRoot}/v1/videos`, {
    method: 'POST',
    headers: {
      Authorization: `Bearer ${apiKey}`,
      'Content-Type': 'application/json',
    },
    body: JSON.stringify({
      model: 'seedance-720p-fast-c47',
      prompt: '海边日落，电影感镜头缓慢向前推进',
      duration: 5,
      resolution: '720p',
      size: '16:9',
      mode_type: 'text2video',
      n: 1,
    }),
  });

  const data = await response.json();
  if (!response.ok) {
    throw new Error(data.message || `HTTP ${response.status}`);
  }
  return data.id || data.task_id;
}
```

## 11. Python 示例

```python
import os
import time
import requests

api_root = "https://token.mewinyou.shop"
api_key = os.environ["VIDEO_API_KEY"]
headers = {"Authorization": f"Bearer {api_key}"}

create_response = requests.post(
    f"{api_root}/v1/videos",
    headers={**headers, "Content-Type": "application/json"},
    json={
        "model": "seedance-720p-fast-c47",
        "prompt": "海边日落，电影感镜头缓慢向前推进",
        "duration": 5,
        "resolution": "720p",
        "size": "16:9",
        "mode_type": "text2video",
        "n": 1,
    },
    timeout=60,
)
create_response.raise_for_status()
task = create_response.json()
task_id = task.get("id") or task["task_id"]

while True:
    status_response = requests.get(
        f"{api_root}/v1/videos/{task_id}",
        headers=headers,
        timeout=30,
    )
    status_response.raise_for_status()
    status_data = status_response.json()
    status = status_data.get("status")

    if status == "completed":
        break
    if status in {"failed", "cancelled"}:
        raise RuntimeError(status_data)

    time.sleep(4)

video_response = requests.get(
    f"{api_root}/v1/videos/{task_id}/content",
    headers=headers,
    timeout=300,
)
video_response.raise_for_status()

with open("output.mp4", "wb") as video_file:
    video_file.write(video_response.content)
```

## 12. 错误处理

错误响应通常包含：

```json
{
  "error": {
    "message": "错误说明",
    "type": "invalid_request_error"
  }
}
```

常见 HTTP 状态码：

| 状态码 | 说明 | 建议处理 |
| ---: | --- | --- |
| `400` | 参数错误、模型与参数组合不支持 | 修正请求，不要原样重试 |
| `401` | API Key 缺失或无效 | 检查 Key 和 Bearer 请求头 |
| `429` | 频率限制或当前上游负载较高 | 使用指数退避稍后重试 |
| `500`、`502`、`503`、`504` | 服务或上游暂时异常 | 记录任务/请求信息并有限重试 |

创建任务请求发生网络超时时，不要立即无限重发。应先根据已取得的任务 ID 查询状态；只有确认任务未创建时，才重新提交，避免重复生成和重复计费。

## 13. 接入检查清单

- Base URL 填写正确，没有重复 `/v1`。
- 使用服务商分配的 API Key，并通过 `Authorization: Bearer` 发送。
- 使用公开模型名称，不使用带冒号的内部模型名。
- `resolution` 与模型名称中的清晰度一致。
- `duration` 符合所选线路限制。
- `mode_type` 与模型能力匹配。
- 图像 URL 可由服务端通过公网访问。
- `n` 固定为 `1`。
- 保存创建响应中的公开任务 ID。
- 每 3-5 秒查询一次状态，完成后再请求 content 接口。
- 为创建、查询和下载分别设置合理超时，并妥善处理非 2xx 响应。
