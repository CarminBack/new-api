// Video models returned by https://y.junlistudio.com/v1/models (kind "video"), checked 2026-10-25.
// Durations come from the site's own validation errors; other limits come from its public model catalog.
// Anything the site does not publish stays undeclared instead of being inferred from the model name.
const R_WIDE = ["16:9", "9:16"];
const R_WAN = ["1:1", "16:9", "9:16", "4:3", "3:2", "3:4", "2:3", "4:5"];
const WAN_STEPS = [4, 5, 6, 8, 10, 12, 15, 30];
function caps(limits) { return Object.assign({ requestFormat: "unified_video_v1", maxAudios: 0 }, limits); }
const CAPABILITIES = {
  "seedance-2.5": caps({ resolutions: ["480p", "720p"], ratios: ["21:9", "16:9", "4:3", "1:1", "3:4", "9:16"], minSeconds: 4, maxSeconds: 25, frames: false, maxImages: 30, maxVideos: 0 }),
  "veo-3.1-1080p": caps({ resolutions: ["1080p"], ratios: R_WIDE, minSeconds: 8, maxSeconds: 8, frames: true, maxVideos: 0 }),
  "veo-3.1-fast-1080p": caps({ resolutions: ["1080p"], ratios: R_WIDE, minSeconds: 8, maxSeconds: 8, frames: false, maxImages: 0, maxVideos: 0 }),
  "veo-3.1-fast": caps({ resolutions: ["720p"], ratios: R_WIDE, minSeconds: 8, maxSeconds: 8, frames: true, maxVideos: 0 }),
  "veo-3.1": caps({ resolutions: ["720p"], ratios: R_WIDE, minSeconds: 8, maxSeconds: 8, frames: true, maxVideos: 0 }),
  "wan3.0-720p-mx10.5.0-gfapi": caps({ resolutions: ["720p"], ratios: R_WAN, minSeconds: 4, maxSeconds: 30, secondsSteps: WAN_STEPS, frames: false, maxVideos: 5 }),
  "wan3.0-480p-mx10.5.0-gfapi": caps({ resolutions: ["480p"], ratios: ["1:1", "16:9", "9:16"], minSeconds: 4, maxSeconds: 30, secondsSteps: WAN_STEPS, frames: false, maxVideos: 5 }),
  "wan3.0-1080p-mx10.5.0-gfapi": caps({ resolutions: ["1080p"], ratios: ["1:1", "16:9", "9:16", "3:2", "4:3", "2:3", "3:4"], minSeconds: 4, maxSeconds: 30, secondsSteps: WAN_STEPS, frames: false, maxVideos: 5 }),
  "minimax-h3-optimized": caps({ ratios: ["16:9", "9:16", "1:1", "4:3", "3:4", "21:9"], minSeconds: 4, maxSeconds: 15, frames: true, maxVideos: 3 }),
  "grok-video": caps({ resolutions: ["720p"], ratios: ["2:3", "3:2", "1:1", "9:16", "16:9"], minSeconds: 6, maxSeconds: 6, frames: false, maxImages: 0, maxVideos: 0 }),
};
const MODELS = Object.keys(CAPABILITIES);

export const meta = {
  apiVersion: 1, key: "junli", name: "Junli Studio Video", icon: "text:JL",
  description: { en: "Junli Studio video generation with structured reference assets", zh: "Junli Studio 视频生成与结构化参考素材" },
  version: "1.0.0", author: { name: "Carmin" }, baseUrl: "https://y.junlistudio.com",
  website: "https://y.junlistudio.com/docs/video",
  models: MODELS,
  fetchMode: "per_task",
  modelDiscovery: "openai",
  videoCapabilities: CAPABILITIES,
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

// Keep aligned with constant.MaxTaskDurationSeconds.
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
function seconds(raw) {
  if ((typeof raw !== "number" && typeof raw !== "string") || !/^[1-9][0-9]{0,3}$/.test(String(raw)) || Number(raw) > MAX_SECONDS) {
    throw new Error("duration must be an integer between 1 and 3600");
  }
  return Number(raw);
}
function mediaRef(ref) {
  if (!ref || typeof ref !== "object" || Array.isArray(ref)) throw new Error("each reference must be an object");
  if (!/^(?:image|video)$/.test(ref.type)) {
    if (ref.type === "audio") throw new Error("Junli audio references are not supported: the audio field name is undocumented");
    throw new Error("reference type must be image or video");
  }
  if (!["reference", "first_frame", "last_frame"].includes(ref.role)) throw new Error("reference role must be reference, first_frame or last_frame");
  if (ref.type !== "image" && ref.role !== "reference") throw new Error("only images support frame roles");
  if (ref.duration_seconds !== undefined && (ref.type !== "video" || typeof ref.duration_seconds !== "number" || !Number.isFinite(ref.duration_seconds) || ref.duration_seconds <= 0 || ref.duration_seconds > MAX_SECONDS)) {
    throw new Error("reference duration_seconds must be between 0 and 3600 for video media");
  }
  const item = { type: ref.type, role: ref.role };
  if (ref.url && typeof ref.url === "object" && ref.url.__fileRef && ref.url.encoding === "publicUrl") {
    item.url = { __fileRef: ref.url.__fileRef, encoding: "publicUrl", mediaKind: ref.type };
  } else item.url = mediaURL(ref.url);
  if (ref.duration_seconds !== undefined) item.duration_seconds = ref.duration_seconds;
  if (ref.name !== undefined) {
    if (!trimmed(ref.name)) throw new Error("reference name must be a non-empty string");
    item.name = trimmed(ref.name);
  }
  return item;
}
// Normalizes unified and legacy request bodies. Output is itself valid input, so decode, billing and submit agree.
function normalizedRequest(req, model) {
  if (!req || typeof req !== "object" || Array.isArray(req)) throw new Error("request body must be an object");
  const body = { model: trimmed(model) || trimmed(req.model), prompt: trimmed(req.prompt) };
  if (!body.model) throw new Error("model is required");
  if (!body.prompt) throw new Error("prompt is required");
  if (req.n !== undefined && req.n !== 1 && req.n !== "1") throw new Error("only n=1 is supported");
  for (const key of ["duration", "seconds"]) {
    if (req[key] === undefined) continue;
    const value = seconds(req[key]);
    if (body.duration !== undefined && body.duration !== value) throw new Error("duration and seconds must match");
    body.duration = value;
  }
  for (const key of ["aspect_ratio", "ratio"]) {
    if (req[key] === undefined) continue;
    const ratio = trimmed(req[key]);
    if (!/^[1-9][0-9]{0,3}:[1-9][0-9]{0,3}$/.test(ratio)) throw new Error("aspect_ratio must be a ratio such as 16:9");
    if (body.aspect_ratio && body.aspect_ratio !== ratio) throw new Error("aspect_ratio and ratio must match");
    body.aspect_ratio = ratio;
  }
  for (const key of ["resolution", "size"]) {
    if (req[key] === undefined) continue;
    const value = trimmed(req[key]);
    if (key === "resolution" && !/^(?:[1-9][0-9]*p|4K)$/.test(value)) throw new Error("resolution must be a tier such as 720p");
    if (key === "size" && !/^[1-9][0-9]{0,4}x[1-9][0-9]{0,4}$/.test(value)) throw new Error("size must be pixel dimensions such as 1280x720");
    body[key] = value;
  }
  const references = [];
  if (req.references !== undefined) {
    if (!Array.isArray(req.references)) throw new Error("references must be an array");
    if (["images", "videos", "audios", "input_reference"].some(function (key) { return req[key] !== undefined; })) {
      throw new Error("references cannot be combined with images, videos, audios or input_reference");
    }
    for (const ref of req.references) references.push(mediaRef(ref));
  } else {
    if (req.audios !== undefined) throw new Error("Junli audio references are not supported: the audio field name is undocumented");
    for (const pair of [["images", "image"], ["videos", "video"]]) {
      if (req[pair[0]] === undefined) continue;
      if (!Array.isArray(req[pair[0]])) throw new Error(pair[0] + " must be an array");
      for (const url of req[pair[0]]) references.push({ type: pair[1], role: "reference", url: mediaURL(url) });
    }
    if (req.input_reference !== undefined) references.push({ type: "image", role: "first_frame", url: mediaURL(req.input_reference) });
  }
  const first = references.filter(function (ref) { return ref.role === "first_frame"; }).length;
  const last = references.filter(function (ref) { return ref.role === "last_frame"; }).length;
  if (first > 1 || last > 1 || (last && !first)) throw new Error("frame references require one first_frame and at most one last_frame");
  if ((first || last) && references.some(function (ref) { return ref.role === "reference"; })) {
    throw new Error("first and last frames cannot be mixed with ordinary reference media");
  }
  if (references.length) body.references = references;
  return body;
}

function wireBody(request) {
  const body = { model: request.model, prompt: request.prompt, response_format: "url" };
  if (request.duration !== undefined) body.seconds = request.duration;
  for (const key of ["aspect_ratio", "resolution", "size"]) if (request[key] !== undefined) body[key] = request[key];
  const refs = request.references || [];
  if (!refs.length) return body;
  if (refs.some(function (ref) { return ref.role !== "reference"; })) {
    body.reference_mode = "frame";
    for (const ref of refs) body[ref.role === "first_frame" ? "start_frame" : "end_frame"] = ref.url;
    return body;
  }
  body.reference_mode = "asset";
  const images = refs.filter(function (ref) { return ref.type === "image"; }).map(function (ref) { return ref.url; });
  const videos = refs.filter(function (ref) { return ref.type === "video"; }).map(function (ref) { return ref.url; });
  if (images.length) body.reference_images = images;
  if (videos.length) body.reference_videos = videos;
  return body;
}
function authHeaders(ctx, extra) {
  return Object.assign({ Authorization: ctx.authHeader || "Bearer " + ctx.apiKey, Accept: "application/json" }, extra || {});
}
export function buildSubmitRequest(ctx) {
  const body = wireBody(normalizedRequest(ctx.requestBody, ctx.upstreamModel));
  return { url: providerVideosURL(ctx.baseUrl), method: "POST", headers: authHeaders(ctx, { "Content-Type": "application/json" }), body: body };
}
export function parseSubmitResponse(ctx, resp) {
  const body = resp.body || {};
  const taskId = trimmed(body.id) || trimmed(body.task_id);
  if (!taskId) throw new Error(trimmed(body.detail) || trimmed(body.error && body.error.message) || "provider response did not contain a task id");
  const result = { taskId: taskId, taskData: body };
  if (body.status === "failed" || body.status === "completed") {
    const terminal = parseTaskResult(ctx, body);
    if (terminal.status === "SUCCESS" || terminal.status === "FAILURE") result.immediate = terminal;
  }
  return result;
}
export function buildQueryRequest(ctx) {
  return { url: providerVideosURL(ctx.baseUrl) + "/" + encodeURIComponent(ctx.taskId), method: "GET", headers: authHeaders(ctx) };
}
function failureReason(body) {
  const error = body.error;
  const detail = error && typeof error === "object" ? [trimmed(error.code), trimmed(error.message)].filter(Boolean).join(": ") : trimmed(error);
  return detail || trimmed(body.fail_reason) || trimmed(body.detail) || "video generation failed";
}
export function parseTaskResult(_ctx, body) {
  body = body || {};
  const states = { queued: "QUEUED", in_progress: "IN_PROGRESS", completed: "SUCCESS", failed: "FAILURE" };
  const status = Object.prototype.hasOwnProperty.call(states, body.status) ? states[body.status] : "UNKNOWN";
  const result = { status: status };
  const progress = body.progress === undefined || body.progress === null ? NaN : Number(String(body.progress).replace("%", ""));
  if (Number.isFinite(progress) && progress >= 0 && progress <= 100) result.progress = progress + "%";
  if (status === "SUCCESS") result.progress = "100%";
  if (status === "FAILURE") result.reason = failureReason(body);
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
  const raw = body.seconds !== undefined ? body.seconds : body.duration;
  if ((typeof raw !== "string" && typeof raw !== "number") || !/^[1-9][0-9]{0,3}$/.test(String(raw)) || Number(raw) > MAX_SECONDS) return null;
  return { seconds: Number(raw) };
}
export function listArtifacts(task) { return task.status === "SUCCESS" ? [{ key: "video", type: "video" }] : []; }
export function buildContentRequest(ctx) {
  if (ctx.artifactKey !== "video") throw new Error("artifact_not_found");
  const headers = authHeaders(ctx);
  delete headers.Accept;
  const incoming = (ctx.clientRequest && ctx.clientRequest.headers) || {};
  for (const name of Object.keys(incoming)) if (name.toLowerCase() === "range" && trimmed(incoming[name])) headers.Range = trimmed(incoming[name]);
  return { url: providerVideosURL(ctx.baseUrl) + "/" + encodeURIComponent(ctx.upstreamTaskId) + "/content", method: ctx.clientRequest.method, headers: headers };
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
      req[key] = ["references", "images", "videos", "audios"].includes(key) ? JSON.parse(values[0]) : values[0];
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
    renderFinal: function (ctx) { return { output: [{ type: "message", status: "completed", role: "assistant", content: [{ type: "output_text", text: responsesVideoText(ctx), annotations: [], logprobs: [] }] }], metadata: { vendor: "junli" } }; },
  },
};
