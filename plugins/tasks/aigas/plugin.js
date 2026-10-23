// Video-capable public models advertising openai-video in /api/pricing, 2026-10-23.
const MODELS = [
    "sd-2-vip-480", "sd-2.5-官方版-480p", "sd-2.0-fast-官方版-480p", "wan3.0-1080p-prime-官方版",
    "grok-video-1.5", "minimax-h3-官方版-768p", "sd-2.0-720P-官转版", "sd-2.5-1080P",
    "sd-2.0-mini-官方版-720p", "sd-2.0-官方版-480p", "wan3.0-480p-prime-官方版", "sd-2.5-480P",
    "sd-2.0-720-900", "minimax-h3-768P", "wan3.0-720p-prime-官方版", "sd-2.0-官方版-720p",
    "sd-2.5-官方版-720p", "minimax-h3-官方版-1080p", "sd-2.5-720-900vip", "sd-2.0-fast-官方版-720p",
    "sd-2.5-720P", "sd-2-vip-720", "sd-2.0-mini-官方版-480p",
  ];

export const meta = {
  apiVersion: 1, key: "aigas", name: "AIGAS Video", icon: "text:AG",
  description: { en: "AIGAS video generation with structured reference assets", zh: "AIGAS 视频生成与结构化参考素材" },
  version: "1.0.2", author: { name: "Carmin" }, baseUrl: "https://www.aigasapi.com",
  website: "https://www.aigasapi.com/docs/api/videos",
  models: MODELS,
  fetchMode: "per_task",
  modelDiscovery: "openai",
  videoCapabilities: Object.fromEntries(MODELS.map(function (model) {
    // Unpublished provider limits are intentionally not inferred from the model name.
    return [model, model === "grok-video-1.5"
      ? { requestFormat: "unified_video_v1", resolutions: ["720p"], ratios: ["16:9", "9:16"], minSeconds: 4, maxSeconds: 15, frames: false, maxImages: 1, maxVideos: 0, maxAudios: 0, requiredImages: 1 }
      : { requestFormat: "unified_video_v1" }];
  })),
  usageSchema: {
    seconds: { type: "number", unit: "second", description: { en: "Video generation unit price", zh: "视频生成单价" } },
    videos: { type: "number", unit: "count", description: { en: "Video generation unit price", zh: "视频生成单价" } },
    video_input: {
      enum: ["none", "video"], description: { en: "Reference video input", zh: "参考视频输入" },
      enumLabels: { none: { en: "No reference video", zh: "无参考视频" }, video: { en: "With reference video", zh: "有参考视频" } },
    },
  },
  usageExamples: [{ label: "8s video", facts: { seconds: 8, videos: 1, video_input: "none" } }],
  protocols: [{ name: "openai_responses", supports: ["stream", "sync", "background"] }, "openai_video"],
};

// Keep aligned with relay/common.MaxTaskDurationSeconds; AIGAS does not document -1.
const MAX_SECONDS = 3600;
function trimmed(value) { return typeof value === "string" ? value.trim() : ""; }
function providerVideosURL(base) {
  base = trimmed(base).replace(/\/+$/, "");
  return /\/v1$/i.test(base) ? base + "/videos" : base + "/v1/videos";
}
function mediaURL(value) {
  const url = trimmed(value);
  if (!/^https?:\/\/[^\s]+$/i.test(url)) throw new Error("reference media must use public HTTP(S) URLs");
  return url;
}
function normalizedRequest(req, model) {
  if (!req || typeof req !== "object" || Array.isArray(req)) throw new Error("request body must be an object");
  const body = { model: trimmed(model) || trimmed(req.model), prompt: trimmed(req.prompt) };
  if (!body.model) throw new Error("model is required");
  if (!body.prompt) throw new Error("prompt is required");
  if (req.n !== undefined && req.n !== 1 && req.n !== "1") throw new Error("only n=1 is supported");
  for (const key of ["duration", "seconds"]) {
    if (req[key] === undefined) continue;
    const raw = req[key];
    if ((typeof raw !== "number" && typeof raw !== "string") || !/^[1-9][0-9]{0,3}$/.test(String(raw)) || Number(raw) > MAX_SECONDS) {
      throw new Error("duration must be an integer between 1 and 3600; AIGAS does not document seconds=-1");
    }
    if (body.duration !== undefined && body.duration !== Number(raw)) throw new Error("duration and seconds must match");
    body.duration = Number(raw);
  }
  for (const key of ["aspect_ratio", "ratio"]) {
    if (req[key] === undefined) continue;
    const ratio = trimmed(req[key]);
    if (!/^[1-9][0-9]{0,3}:[1-9][0-9]{0,3}$/.test(ratio)) throw new Error("aspect_ratio must be a ratio such as 16:9");
    if (body.aspect_ratio && body.aspect_ratio !== ratio) throw new Error("aspect_ratio and ratio must match");
    body.aspect_ratio = ratio;
  }
  if (req.orientation !== undefined) {
    if (!["landscape", "portrait", "square"].includes(req.orientation)) throw new Error("orientation must be landscape, portrait or square");
    const ratio = { landscape: "16:9", portrait: "9:16", square: "1:1" }[req.orientation];
    if (!ratio || (body.aspect_ratio && body.aspect_ratio !== ratio)) throw new Error("orientation must agree with aspect_ratio");
    body.aspect_ratio = ratio;
  }
  for (const key of ["resolution", "size", "out_task_id"]) {
    if (req[key] === undefined) continue;
    const value = trimmed(req[key]);
    if (!value) throw new Error(key + " must be a non-empty string");
    if (key === "size" && !/^[1-9][0-9]{0,4}x[1-9][0-9]{0,4}$/.test(value)) throw new Error("size must be pixel dimensions such as 1280x720");
    if (key === "out_task_id" && [...value].length > 64) throw new Error("out_task_id must be at most 64 characters");
    body[key] = value;
  }
  if (req.watermark !== undefined) {
    if (typeof req.watermark !== "boolean") throw new Error("watermark must be a boolean");
    body.watermark = req.watermark;
  }
  const references = [];
  if (req.references !== undefined) {
    if (!Array.isArray(req.references)) throw new Error("references must be an array");
    if (["images", "videos", "audios", "input_reference"].some(function (key) { return req[key] !== undefined; })) {
      throw new Error("references cannot be combined with images, videos, audios or input_reference");
    }
    for (const ref of req.references) {
      if (!ref || typeof ref !== "object" || Array.isArray(ref)) throw new Error("each reference must be an object");
      if (!["image", "video", "audio"].includes(ref.type)) throw new Error("reference type must be image, video or audio");
      if (!["reference", "first_frame", "last_frame"].includes(ref.role)) throw new Error("reference role must be reference, first_frame or last_frame");
      if (ref.type !== "image" && ref.role !== "reference") throw new Error("only images support frame roles");
      if (ref.duration_seconds !== undefined && (ref.type !== "video" || typeof ref.duration_seconds !== "number" || !Number.isFinite(ref.duration_seconds) || ref.duration_seconds <= 0 || ref.duration_seconds > MAX_SECONDS)) {
        throw new Error("reference duration_seconds must be between 0 and 3600 for video media");
      }
      const item = { type: ref.type, role: ref.role };
      if (ref.url && typeof ref.url === "object" && ref.url.__fileRef && ref.url.encoding === "publicUrl") {
        item.url = { __fileRef: ref.url.__fileRef, encoding: "publicUrl", mediaKind: ref.type };
      } else item.url = mediaURL(ref.url);
      if (ref.name !== undefined) {
        if (!trimmed(ref.name)) throw new Error("reference name must be a non-empty string");
        item.name = trimmed(ref.name);
      }
      references.push(item);
    }
  } else {
    for (const pair of [["images", "image"], ["videos", "video"], ["audios", "audio"]]) {
      if (req[pair[0]] === undefined) continue;
      if (!Array.isArray(req[pair[0]])) throw new Error(pair[0] + " must be an array");
      for (const url of req[pair[0]]) references.push({ type: pair[1], role: "reference", url: mediaURL(url) });
    }
    if (req.input_reference !== undefined) references.push({ type: "image", role: "first_frame", url: mediaURL(req.input_reference) });
  }
  const first = references.filter(function (ref) { return ref.role === "first_frame"; }).length;
  const last = references.filter(function (ref) { return ref.role === "last_frame"; }).length;
  if (first > 1 || last > 1 || (last && !first)) throw new Error("frame references require one first_frame and at most one last_frame");
  if ((first || last) && references.some(function (ref) { return ref.type === "image" && ref.role === "reference"; })) {
    throw new Error("frame roles cannot be mixed with ordinary image references");
  }
  if (references.length) body.references = references;
  return body;
}

export function buildSubmitRequest(ctx) {
  const body = normalizedRequest(ctx.requestBody, ctx.upstreamModel);
  // A stable caller-supplied ID wins; otherwise use the host's public ID when available.
  if (!body.out_task_id && ctx.publicTaskId) body.out_task_id = ctx.publicTaskId;
  return { url: providerVideosURL(ctx.baseUrl), method: "POST", headers: { Authorization: ctx.authHeader || "Bearer " + ctx.apiKey, Accept: "application/json", "Content-Type": "application/json" }, body: body };
}
export function parseSubmitResponse(ctx, resp) {
  const body = resp.body || {};
  const taskId = trimmed(body.id) || trimmed(body.task_id);
  if (!taskId) throw new Error(trimmed(body.error && body.error.message) || "provider response did not contain a task id");
  const result = { taskId: taskId, taskData: body };
  if (body.status === "failed" || body.status === "completed") {
    const terminal = parseTaskResult(ctx, body);
    if (terminal.status === "SUCCESS" || terminal.status === "FAILURE") result.immediate = terminal;
  }
  return result;
}
export function buildQueryRequest(ctx) {
  return { url: providerVideosURL(ctx.baseUrl) + "/" + encodeURIComponent(ctx.taskId), method: "GET", headers: { Authorization: ctx.authHeader || "Bearer " + ctx.apiKey, Accept: "application/json" } };
}
function resultURL(body) { return trimmed(body && body.metadata && body.metadata.url); }
export function parseTaskResult(_ctx, body) {
  body = body || {};
  const states = { queued: "QUEUED", in_progress: "IN_PROGRESS", completed: "SUCCESS", failed: "FAILURE" };
  const status = states[body.status];
  const result = { status: typeof status === "string" ? status : "UNKNOWN" };
  const progress = body.progress === undefined ? NaN : Number(String(body.progress).replace("%", ""));
  if (Number.isFinite(progress) && progress >= 0 && progress <= 100) result.progress = progress + "%";
  if (result.status === "SUCCESS") {
    const url = resultURL(body);
    if (!/^https?:\/\/[^\s]+$/i.test(url)) return { status: "UNKNOWN", reason: "completed task did not contain a valid metadata.url" };
    result.url = url;
    result.progress = "100%";
  }
  if (result.status === "FAILURE") result.reason = [trimmed(body.error && body.error.code), trimmed(body.error && body.error.message) || trimmed(body.fail_reason)].filter(Boolean).join(": ") || "video generation failed";
  return result;
}
export function extractUsage(ctx) {
  if (ctx.usagePurpose === "billing_ratios") return null;
  const body = normalizedRequest(ctx.requestBody, ctx.upstreamModel);
  if (body.duration === undefined) throw new Error("provide duration or seconds explicitly so billing never guesses the model default");
  return { seconds: body.duration, videos: 1, video_input: (body.references || []).some(function (ref) { return ref.type === "video"; }) ? "video" : "none" };
}
export function extractUsageOnComplete(_ctx, result, body) {
  if (!result || result.status !== "SUCCESS" || !body) return null;
  const raw = body.seconds;
  if ((typeof raw !== "string" && typeof raw !== "number") || !/^[1-9][0-9]{0,3}$/.test(String(raw)) || Number(raw) > MAX_SECONDS) return null;
  return { seconds: Number(raw) };
}
export function listArtifacts(task) { return task.status === "SUCCESS" && /^https?:\/\//i.test(resultURL(task.data)) ? [{ key: "video", type: "video" }] : []; }
export function buildContentRequest(ctx) {
  if (ctx.artifactKey !== "video") throw new Error("artifact_not_found");
  return { url: mediaURL(resultURL(ctx.data)), method: ctx.clientRequest.method, credentialless: true };
}

function decodeVideo(ctx) {
  const body = ctx.body || {};
  let req;
  if (body.kind === "json") req = body.value;
  else if (body.kind === "multipart") {
    req = {};
    for (const key of Object.keys(body.fields || {})) {
      const values = body.fields[key];
      if (values.length !== 1) throw new Error(key + " must be provided once");
      req[key] = ["references", "images", "videos", "audios", "watermark"].includes(key) ? JSON.parse(values[0]) : values[0];
    }
    const files = body.files || [];
    if (files.length) {
      if (files.length !== 1 || files[0].field !== "input_reference") throw new Error("only one input_reference file is supported; use references URLs for other media");
      if (["references", "images", "videos", "audios", "input_reference"].some(function (key) { return req[key] !== undefined; })) throw new Error("input_reference upload cannot be combined with other references");
      req.references = [{ type: "image", role: "first_frame", url: { __fileRef: files[0].ref, encoding: "publicUrl", mediaKind: "image" } }];
    }
  } else throw new Error("JSON or multipart body required");
  const requestBody = normalizedRequest(req, ctx.model || (req && req.model));
  return { kind: "submit", model: requestBody.model, action: "generate", requestBody: requestBody };
}
function responsesVideoText(ctx) {
  const url = trimmed(ctx.artifacts && ctx.artifacts.video && ctx.artifacts.video.url);
  if (!url) throw new Error("video artifact is unavailable");
  return '<video controls src="' + url.replace(/&/g, "&amp;").replace(/"/g, "&quot;").replace(/</g, "&lt;").replace(/>/g, "&gt;") + '"></video>';
}
export const protocols = {
  openai_video: {
    decodeRequest: decodeVideo,
    render: function (_ctx, task) { return task.status === "FAILURE" ? { error: { code: "task_failed", message: task.fail_reason || "video generation failed" } } : {}; },
  },
  openai_responses: {
    decodeRequest: function (ctx) {
      if (!ctx.body || ctx.body.kind !== "json") throw new Error("JSON body required");
      const req = ctx.body.value;
      if (!req || typeof req !== "object" || Array.isArray(req)) throw new Error("request body must be an object");
      if (req.input !== undefined && typeof req.input !== "string" && !Array.isArray(req.input)) throw new Error("input must be a string or array");
      const texts = [], images = [];
      if (typeof req.input === "string") texts.push(req.input);
      else for (const item of req.input || []) {
        if (typeof item === "string") { texts.push(item); continue; }
        if (!item || typeof item !== "object") continue;
        for (const part of item.content === undefined ? [item] : Array.isArray(item.content) ? item.content : [item.content]) {
          if (typeof part === "string") texts.push(part);
          else if (part && typeof part === "object") {
            if (["input_text", "text"].includes(part.type) && typeof part.text === "string") texts.push(part.text);
            if (["input_image", "image_url"].includes(part.type)) images.push(part.image_url && typeof part.image_url === "object" ? part.image_url.url : part.image_url);
          }
        }
      }
      const request = Object.assign({}, req, { prompt: texts.join("\n").trim() || req.prompt });
      if (images.length) {
        if (request.references !== undefined) throw new Error("Responses input images cannot be combined with references; use explicit reference roles");
        if (request.images !== undefined && !Array.isArray(request.images)) throw new Error("images must be an array");
        request.images = (request.images || []).concat(images);
      }
      return decodeVideo({ model: ctx.model, body: { kind: "json", value: request } });
    },
    renderEvents: function (ctx, task, previous) {
      const status = String(task.status || "UNKNOWN").toUpperCase();
      const value = Number(String(task.progress || "").replace("%", ""));
      const progress = Number.isFinite(value) && value >= 0 && value <= 100 ? value : null;
      const state = { status: status, progress: progress };
      if (status === "SUCCESS") return { events: previous && previous.status === status ? [] : [{ type: "output", data: responsesVideoText(ctx) }], state: state, done: true };
      if (status === "FAILURE") return { events: [{ type: "error", code: "task_failed", message: task.fail_reason || "video generation failed" }], state: state, done: true };
      if (previous && previous.status === status && previous.progress === progress) return { events: [], state: state, done: false };
      const event = { type: "progress", message: status.toLowerCase() };
      if (progress !== null) event.progress = progress;
      return { events: [event], state: state, done: false };
    },
    renderFinal: function (ctx) { return { output: [{ type: "message", status: "completed", role: "assistant", content: [{ type: "output_text", text: responsesVideoText(ctx), annotations: [], logprobs: [] }] }], metadata: { vendor: "aigas" } }; },
  },
};