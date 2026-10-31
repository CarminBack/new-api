// Video models from the public catalog https://www.yeepay.top/api/models, checked 2026-10-25.
// Row: model, resolutions (null = not a plain "<n>p" tier, so unknown), min seconds, max seconds,
// allowed durations when the list has gaps, max images, max audios, max videos, billing unit,
// whether reference video seconds are billed. Limits the catalog does not publish stay undeclared.
function range(from, to) { const out = []; for (let n = from; n <= to; n++) out.push(n); return out; }
const WAN_STEPS = range(5, 27).concat([29, 30]);
const MINIMAX_STEPS = [5, 6, 7].concat(range(9, 15));
const SPECIAL_STEPS = range(5, 18).concat(range(21, 30));
const FIVE_STEPS = [5, 10, 15];
const ROWS = [
  ["Xseadanco2.0", ["720p"], 5, 15, null, 9, 3, 3, "request", false],
  ["XXseadanco2.0M", ["720p"], 5, 15, null, 9, 3, 3, "second", false],
  ["XXseedacn2.5", ["720p"], 5, 30, null, 30, 10, 10, "second", true],
  ["wan3/1080", ["1080p", "720p"], 5, 30, WAN_STEPS, 10, 5, 5, "request", false],
  ["Aseedance2.0", ["720p"], 5, 15, null, 9, 3, 3, "request", false],
  ["wan3-720/1080", ["1080p", "720p"], 5, 30, null, 10, 5, 5, "request", false],
  ["A-SD2.0", ["720p"], 5, 15, FIVE_STEPS, 9, 3, 3, "request", false],
  ["Fseadanco2.0mini", ["720p"], 5, 15, null, 9, 3, 0, "request", false],
  ["Xminimex-h3", null, 5, 15, null, 9, 3, 3, "second", false],
  ["TTsedanco2.0-480", ["480p"], 5, 15, null, 9, 3, 3, "second", false],
  ["TTsedanco2.0-720", ["720p"], 5, 15, null, 9, 3, 3, "second", false],
  ["TTseadanco2.5-1080", ["1080p"], 5, 30, null, 30, 10, 10, "second", false],
  ["TTsedanco2.5-480", ["480p"], 5, 30, null, 30, 10, 10, "second", false],
  ["TTseadanco2.5-720", ["720p"], 5, 30, null, 30, 10, 10, "second", false],
  ["MinimaxH3-768", ["768p"], 5, 15, MINIMAX_STEPS, 9, 3, 3, "request", false],
  ["OOsd2.5-30秒", ["720p"], 30, 30, null, 9, 0, 0, "request", false],
  ["OOsd2.5", ["720p"], 15, 15, null, 9, 0, 0, "request", false],
  ["00sd2.0", ["720p"], 5, 15, FIVE_STEPS, 9, 0, 0, "request", false],
  ["特价seadanco2.5-1080", ["1080p"], 5, 30, SPECIAL_STEPS, 9, 3, 0, "request", false],
  ["TTP-grok", ["720p", "480p"], 6, 15, null, 5, 0, 0, "request", false],
  ["sedanco2.0-300秒必出", ["480p"], 15, 15, null, 9, 3, 0, "request", false],
];
const CAPABILITIES = {};
const SECOND_MODELS = [];
const REFERENCE_SECOND_MODELS = [];
const MODELS = ROWS.map(function (row) {
  const caps = { requestFormat: "unified_video_v1", minSeconds: row[2], maxSeconds: row[3], maxImages: row[5], maxAudios: row[6], maxVideos: row[7] };
  if (row[1]) caps.resolutions = row[1];
  if (row[4]) caps.secondsSteps = row[4];
  if (row[9]) caps.referenceDurationRequired = true;
  CAPABILITIES[row[0]] = caps;
  if (row[8] === "second") (row[9] ? REFERENCE_SECOND_MODELS : SECOND_MODELS).push(row[0]);
  return row[0];
});
const UNIT_PRICE = { en: "Video generation unit price", zh: "视频生成单价" };
const VIDEO_INPUT = {
  enum: ["none", "video"], description: { en: "Reference video input", zh: "参考视频输入" },
  enumLabels: { none: { en: "No reference video", zh: "无参考视频" }, video: { en: "With reference video", zh: "有参考视频" } },
};
// The catalog prices these models per request, per generated second, or per generated plus reference-video second.
const REQUEST_USAGE = { videos: { type: "number", unit: "count", description: UNIT_PRICE } };
const SECOND_USAGE = { seconds: { type: "number", unit: "second", description: UNIT_PRICE }, video_input: VIDEO_INPUT };
const REFERENCE_USAGE = Object.assign({}, SECOND_USAGE, {
  reference_seconds: { type: "number", unit: "second", description: { en: "Reference video unit price", zh: "参考视频单价" } },
});

export const meta = {
  apiVersion: 1, key: "yeepay", name: "Yeepay Video", icon: "text:YP",
  description: { en: "Yeepay video generation with structured reference assets", zh: "Yeepay 视频生成与结构化参考素材" },
  version: "1.0.0", author: { name: "Carmin" }, baseUrl: "https://www.yeepay.top",
  website: "https://www.yeepay.top/api-docs",
  models: MODELS,
  fetchMode: "per_task",
  modelDiscovery: "openai",
  videoCapabilities: CAPABILITIES,
  usageSchema: REQUEST_USAGE,
  usageExamples: [{ label: "Per request", facts: { videos: 1 } }],
  usageProfiles: [
    { models: SECOND_MODELS, schema: SECOND_USAGE, examples: [{ label: "8s video", facts: { seconds: 8, video_input: "none" } }] },
    { models: REFERENCE_SECOND_MODELS, schema: REFERENCE_USAGE, examples: [
      { label: "8s video", facts: { seconds: 8, reference_seconds: 0, video_input: "none" } },
      { label: "8s video with 5s reference", facts: { seconds: 8, reference_seconds: 5, video_input: "video" } },
    ] },
  ],
  protocols: [{ name: "openai_responses", supports: ["stream", "sync", "background"] }, "openai_video"],
};

// Keep aligned with constant.MaxTaskDurationSeconds.
const MAX_SECONDS = 3600;
function trimmed(value) { return typeof value === "string" ? value.trim() : ""; }
// Accepts the site root or a base that already ends in /v1; paths below always add exactly one /v1.
function apiRoot(base) { return trimmed(base).replace(/\/+$/, "").replace(/\/v1$/i, ""); }
function mediaURL(value) {
  const url = trimmed(value);
  if (!/^https:\/\/[^\s]+$/i.test(url)) throw new Error("reference media must use public HTTPS URLs");
  return url;
}
function wholeSeconds(raw) {
  if ((typeof raw !== "number" && typeof raw !== "string") || !/^[1-9][0-9]{0,3}$/.test(String(raw)) || Number(raw) > MAX_SECONDS) {
    throw new Error("duration must be an integer between 1 and 3600");
  }
  return Number(raw);
}
function gcd(a, b) { return b ? gcd(b, a % b) : a; }
// "1280x720" means a 16:9 request at the 720p tier (short side), the same reading the other video plugins use.
function sizeTraits(raw) {
  const match = /^([1-9][0-9]{0,4})x([1-9][0-9]{0,4})$/i.exec(trimmed(raw));
  if (!match) throw new Error("size must look like 1280x720");
  const width = Number(match[1]), height = Number(match[2]), divisor = gcd(width, height);
  return { ratio: width / divisor + ":" + height / divisor, resolution: Math.min(width, height) + "p" };
}
function mediaRef(ref) {
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
  if (ref.duration_seconds !== undefined) item.duration_seconds = ref.duration_seconds;
  if (ref.name !== undefined) {
    if (!trimmed(ref.name)) throw new Error("reference name must be a non-empty string");
    item.name = trimmed(ref.name);
  }
  return item;
}
// Normalizes unified and legacy request bodies. Output is itself valid input, so decode, billing and submit agree.
// Yeepay does not accept size next to ratio/resolution, so size is translated into them here and never sent.
function normalizedRequest(req, model) {
  if (!req || typeof req !== "object" || Array.isArray(req)) throw new Error("request body must be an object");
  const body = { model: trimmed(model) || trimmed(req.model), prompt: trimmed(req.prompt) };
  if (!body.model) throw new Error("model is required");
  if (!body.prompt) throw new Error("prompt is required");
  if (req.n !== undefined && req.n !== 1 && req.n !== "1") throw new Error("only n=1 is supported");
  for (const key of ["duration", "seconds"]) {
    if (req[key] === undefined) continue;
    const value = wholeSeconds(req[key]);
    if (body.duration !== undefined && body.duration !== value) throw new Error("duration and seconds must match");
    body.duration = value;
  }
  for (const key of ["aspect_ratio", "ratio"]) {
    if (req[key] === undefined) continue;
    const ratio = trimmed(req[key]);
    if (!/^[1-9][0-9]{0,3}:[1-9][0-9]{0,3}$/.test(ratio)) throw new Error("aspect_ratio must be a ratio such as 16:9");
    if (body.ratio && body.ratio !== ratio) throw new Error("aspect_ratio and ratio must match");
    body.ratio = ratio;
  }
  if (req.resolution !== undefined) {
    const resolution = trimmed(req.resolution);
    if (!/^(?:[1-9][0-9]*p|[1-9]k)$/i.test(resolution)) throw new Error("resolution must be a tier such as 720p");
    body.resolution = resolution;
  }
  if (req.size !== undefined) {
    const traits = sizeTraits(req.size);
    if (body.ratio && body.ratio !== traits.ratio) throw new Error("size conflicts with aspect_ratio");
    if (body.resolution && body.resolution.toLowerCase() !== traits.resolution) throw new Error("size conflicts with resolution");
    body.ratio = traits.ratio;
    body.resolution = body.resolution || traits.resolution;
  }
  for (const key of ["quality", "negative_prompt"]) {
    if (req[key] === undefined) continue;
    const value = trimmed(req[key]);
    if (!value) throw new Error(key + " must be a non-empty string");
    body[key] = value;
  }
  if (req.generate_audio !== undefined) {
    if (typeof req.generate_audio !== "boolean") throw new Error("generate_audio must be a boolean");
    body.generate_audio = req.generate_audio;
  }
  const references = [];
  if (req.references !== undefined) {
    if (!Array.isArray(req.references)) throw new Error("references must be an array");
    if (["images", "videos", "audios", "input_reference"].some(function (key) { return req[key] !== undefined; })) {
      throw new Error("references cannot be combined with images, videos, audios or input_reference");
    }
    for (const ref of req.references) references.push(mediaRef(ref));
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
  if ((first || last) && references.some(function (ref) { return ref.role === "reference"; })) {
    throw new Error("first and last frames cannot be mixed with ordinary reference media");
  }
  if (references.length) body.references = references;
  return body;
}

function wireBody(request) {
  const body = { model: request.model, prompt: request.prompt };
  if (request.duration !== undefined) body.duration = request.duration;
  for (const key of ["ratio", "resolution", "quality", "negative_prompt", "generate_audio"]) if (request[key] !== undefined) body[key] = request[key];
  const urls = function (type) {
    return (request.references || []).filter(function (ref) { return ref.type === type && ref.role === "reference"; }).map(function (ref) { return ref.url; });
  };
  for (const pair of [["images", "image"], ["reference_videos", "video"], ["reference_audios", "audio"]]) {
    const list = urls(pair[1]);
    if (list.length) body[pair[0]] = list;
  }
  for (const ref of request.references || []) {
    if (ref.role === "first_frame") body.start_frame = ref.url;
    if (ref.role === "last_frame") body.end_frame = ref.url;
  }
  return body;
}
function authHeaders(ctx, extra) {
  return Object.assign({ Authorization: ctx.authHeader || "Bearer " + ctx.apiKey, Accept: "application/json" }, extra || {});
}
function failureMessage(body) {
  const error = body && body.error;
  if (error && typeof error === "object") return [trimmed(error.type), trimmed(error.message)].filter(Boolean).join(": ");
  return trimmed(error) || trimmed(body && body.message);
}
export function buildSubmitRequest(ctx) {
  const body = wireBody(normalizedRequest(ctx.requestBody, ctx.upstreamModel));
  const headers = authHeaders(ctx, { "Content-Type": "application/json" });
  // Retries of the same host task reuse the key, so a repeated create returns the original order instead of billing twice.
  if (trimmed(ctx.publicTaskId) && trimmed(ctx.publicTaskId).length <= 200) headers["Idempotency-Key"] = trimmed(ctx.publicTaskId);
  return { url: apiRoot(ctx.baseUrl) + "/v1/videos", method: "POST", headers: headers, body: body };
}
export function parseSubmitResponse(_ctx, resp) {
  const body = resp.body || {};
  const taskId = trimmed(body.task_id) || trimmed(body.id);
  if (!taskId || body.success === false || (body.code !== undefined && body.code !== 0)) {
    throw new Error(failureMessage(body) || "provider response did not contain a task id");
  }
  return { taskId: taskId, taskData: body };
}
export function buildQueryRequest(ctx) {
  return { url: apiRoot(ctx.baseUrl) + "/v1/tasks/" + encodeURIComponent(ctx.taskId), method: "GET", headers: authHeaders(ctx) };
}
function resultURL(body) {
  for (const key of ["video_url", "download_url", "url", "result_url"]) if (/^https:\/\/[^\s]+$/i.test(trimmed(body && body[key]))) return trimmed(body[key]);
  return "";
}
export function parseTaskResult(_ctx, body) {
  body = body || {};
  const states = { processing: "IN_PROGRESS", succeeded: "SUCCESS", failed: "FAILURE" };
  const status = Object.prototype.hasOwnProperty.call(states, body.status) ? states[body.status] : "UNKNOWN";
  const result = { status: status };
  if (status === "SUCCESS") {
    // A success without a deliverable is not a success: keep the task unresolved instead of charging for nothing.
    if (!resultURL(body)) return { status: "UNKNOWN", reason: "succeeded task did not contain a video address" };
    result.progress = "100%";
  }
  if (status === "FAILURE") result.reason = [trimmed(body.error_code), trimmed(body.error_detail)].filter(Boolean).join(": ") || failureMessage(body) || "video generation failed";
  return result;
}
export function extractUsage(ctx) {
  if (ctx.usagePurpose === "billing_ratios") return null;
  const body = normalizedRequest(ctx.requestBody, ctx.upstreamModel);
  const refs = body.references || [];
  const videos = refs.filter(function (ref) { return ref.type === "video"; });
  const priced = SECOND_MODELS.includes(body.model) || REFERENCE_SECOND_MODELS.includes(body.model);
  if (!priced) return { videos: 1 };
  if (body.duration === undefined) throw new Error("provide duration or seconds explicitly so billing never guesses the model default");
  const facts = { seconds: body.duration, video_input: videos.length ? "video" : "none" };
  if (REFERENCE_SECOND_MODELS.includes(body.model)) {
    // This model bills reference video seconds, so each source duration must be known before submission.
    let total = 0;
    for (const ref of videos) {
      if (typeof ref.duration_seconds !== "number") throw new Error("this model bills reference video seconds; provide duration_seconds for every video reference");
      total += ref.duration_seconds;
    }
    if (total > MAX_SECONDS) throw new Error("total reference video duration must not exceed 3600 seconds");
    facts.reference_seconds = total;
  }
  return facts;
}
export function extractUsageOnComplete() { return null; }
export function listArtifacts(task) { return task.status === "SUCCESS" && resultURL(task.data) ? [{ key: "video", type: "video" }] : []; }
export function buildContentRequest(ctx) {
  if (ctx.artifactKey !== "video") throw new Error("artifact_not_found");
  // The platform serves finished videos without credentials and supports Range; the host forwards the client's Range.
  return { url: apiRoot(ctx.baseUrl) + "/api/video-content/" + encodeURIComponent(ctx.upstreamTaskId), method: ctx.clientRequest.method, credentialless: true };
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
    renderFinal: function (ctx) { return { output: [{ type: "message", status: "completed", role: "assistant", content: [{ type: "output_text", text: responsesVideoText(ctx), annotations: [], logprobs: [] }] }], metadata: { vendor: "yeepay" } }; },
  },
};
