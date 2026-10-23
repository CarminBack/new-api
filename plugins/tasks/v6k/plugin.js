// Public video model IDs and billing units from V6K /api/pricing, 2026-10-22.
const SECOND_MODELS = [
  "TTseadanco2.5-720", "TTseadanco2.0480", "sedanco2.5-720-CY",
  "TTseadanco 2.0 Fast/480", "TTseadanco 2.0 Fast/720", "XXsedanco2.5S",
  "GGsedanco2.5/480", "GGseadanco2.5/720", "XXsedanco2.0S",
  "TTseadanco2.5-1080", "seadano2.5-480-CY", "TTseadanco2.0-720", "TTseadanco2.5-480",
];
const COUNT_MODELS = [
  "JJseadanco2.52", "XXsedanco2.0", "video-QY", "seedance-2.5-CY", "无限制-Video",
  "JJsedanco2.54", "seedance-2.0-mini-720p", "grok--1.5", "doolaa2.0", "sd2.0-mini-CY", "JSadanco2.0",
];
const VIDEO_INPUT = {
  enum: ["none", "video"],
  enumLabels: { none: { en: "No reference video", zh: "无参考视频" }, video: { en: "With reference video", zh: "有参考视频" } },
  description: { en: "Reference video input", zh: "参考视频输入" },
};
const COUNT_USAGE = {
  video_count: { type: "number", unit: "count", description: { en: "Video generation unit price", zh: "视频生成单价" } },
  video_input: VIDEO_INPUT,
};
const SECOND_USAGE = {
  video_seconds: { type: "number", unit: "second", description: { en: "Video generation unit price", zh: "视频生成单价" } },
  video_input: VIDEO_INPUT,
};
const DURATION_FIELDS = ["seconds", "duration", "duration_seconds", "durationSeconds", "video_duration"];
// Match relay/common.MaxTaskDurationSeconds. -1 is a wire sentinel, never usage.
const MAX_SECONDS = 3600;

export const meta = {
  apiVersion: 1, key: "v6k", name: "V6K Video", icon: "text:V6",
  description: { en: "V6K video generation and reference video editing", zh: "V6K 视频生成与参考视频编辑" },
  version: "1.0.1", author: { name: "Carmin" },
  baseUrl: "https://www.v6k.top", website: "https://www.v6k.top/docs?guide=openai&category=video",
  models: SECOND_MODELS.concat(COUNT_MODELS), fetchMode: "per_task",
  videoCapabilities: SECOND_MODELS.concat(COUNT_MODELS).reduce(function (result, model) {
    result[model] = { requestFormat: "unified_video_v1", frames: false };
    if (SECOND_MODELS.includes(model)) {
      result[model].maxVideos = 1;
      result[model].referenceDurationRequired = true;
    }
    return result;
  }, {}),
  usageSchema: COUNT_USAGE,
  usageExamples: [{ label: "Video", facts: { video_count: 1, video_input: "none" } }],
  usageProfiles: [{ models: SECOND_MODELS, schema: SECOND_USAGE, examples: [{ label: "8s video", facts: { video_seconds: 8, video_input: "none" } }] }],
  protocols: [{ name: "openai_responses", supports: ["stream", "sync", "background"] }, "openai_video"],
};

function trimmed(value) { return typeof value === "string" ? value.trim() : ""; }

function videoURL(base) {
  base = trimmed(base).replace(/\/+$/, "");
  return /\/v1$/i.test(base) ? base + "/videos" : base + "/v1/videos";
}

function mediaList(value, name) {
  if (value === undefined) return [];
  if (!Array.isArray(value)) throw new Error(name + " must be an array");
  return value.map(function (item) {
    const object = item && typeof item === "object" && !Array.isArray(item);
    const url = trimmed(object ? item.url : item);
    if (!/^https?:\/\/[^\s]+$/i.test(url)) throw new Error(name + " must contain public HTTP(S) URLs");
    if (!object) return url;
    const result = { url: url };
    if (item.duration_seconds !== undefined) {
      const duration = item.duration_seconds;
      if (typeof duration !== "number" || !Number.isFinite(duration) || duration <= 0 || duration > MAX_SECONDS) {
        throw new Error("reference duration_seconds must be between 0 and 3600");
      }
      result.duration_seconds = duration;
    }
    return result;
  });
}

function normalizedRequest(req, model, files) {
  if (!req || typeof req !== "object" || Array.isArray(req)) throw new Error("request body must be an object");
  if (req.references !== undefined) {
    if (!Array.isArray(req.references)) throw new Error("references must be an array");
    const legacy = ["images", "videos", "audios", "reference_image_urls", "reference_video_urls", "reference_audio_urls", "input_reference"];
    if (legacy.some(function (key) { return req[key] !== undefined; }) || (files || []).length) throw new Error("references cannot be combined with legacy reference media or uploads");
    req = Object.assign({}, req);
    const media = { images: [], videos: [], audios: [] };
    let firstFrame;
    for (const ref of req.references) {
      if (!ref || typeof ref !== "object" || Array.isArray(ref)) throw new Error("each reference must be an object");
      if (!["image", "video", "audio"].includes(ref.type)) throw new Error("reference type must be image, video or audio");
      if (!["reference", "first_frame", "last_frame"].includes(ref.role)) throw new Error("reference role must be reference, first_frame or last_frame");
      if (ref.type !== "image" && ref.role !== "reference") throw new Error("only images support frame roles");
      // The V6K contract exposes one input_reference, not an explicit last-frame field.
      // Never silently reinterpret a requested frame as an ordinary reference image.
      if (ref.role === "last_frame") throw new Error("last_frame is not supported by the V6K adapter");
      if (ref.duration_seconds !== undefined && (ref.type !== "video" || typeof ref.duration_seconds !== "number" || !Number.isFinite(ref.duration_seconds) || ref.duration_seconds <= 0 || ref.duration_seconds > MAX_SECONDS)) {
        throw new Error("reference duration_seconds must be between 0 and 3600 for video media");
      }
      if (typeof ref.url !== "string") throw new Error("reference url must be a public HTTP(S) URL string");
      const item = mediaList([ref.url], "references")[0];
      if (ref.role === "first_frame") {
        if (firstFrame) throw new Error("only one first_frame is supported");
        firstFrame = item;
      } else {
        const key = { image: "images", video: "videos", audio: "audios" }[ref.type];
        media[key].push(ref.duration_seconds === undefined ? item : { url: item, duration_seconds: ref.duration_seconds });
      }
    }
    if (firstFrame) {
      if (media.images.length) throw new Error("frame roles cannot be mixed with ordinary image references");
      req.input_reference = firstFrame;
    }
    for (const key of ["images", "videos", "audios"]) {
      if (media[key].length) req[key] = media[key];
    }
  }
  const result = { model: trimmed(model) || trimmed(req.model), prompt: trimmed(req.prompt) };
  if (!result.model) throw new Error("model is required");
  if (!result.prompt) throw new Error("prompt is required");
  if (req.n !== undefined && req.n !== 1 && req.n !== "1") throw new Error("only n=1 is supported");
  const metadata = req.metadata;
  if (metadata !== undefined) {
    if (!metadata || typeof metadata !== "object" || Array.isArray(metadata)) throw new Error("metadata must be an object");
    for (const key of ["model", "n"].concat(DURATION_FIELDS)) {
      if (Object.prototype.hasOwnProperty.call(metadata, key)) throw new Error("metadata must not override " + key);
    }
    result.metadata = metadata;
  }
  for (const pair of [["images", "reference_image_urls"], ["videos", "reference_video_urls"], ["audios", "reference_audio_urls"]]) {
    const first = mediaList(req[pair[0]], pair[0]);
    const second = mediaList(req[pair[1]], pair[1]);
    if (req[pair[0]] !== undefined && req[pair[1]] !== undefined && JSON.stringify(first) !== JSON.stringify(second)) {
      throw new Error(pair.join(" and ") + " must match");
    }
    const values = req[pair[0]] !== undefined ? first : second;
    if (values.length) result[pair[0]] = values;
  }
  if (req.input_reference !== undefined) {
    let value = req.input_reference;
    if (Array.isArray(value)) {
      if (value.length !== 1) throw new Error("input_reference requires exactly one image");
      value = value[0];
    }
    if (value && typeof value === "object") value = value.url || (value.image_url && (value.image_url.url || value.image_url));
    result.input_reference = mediaList([value], "input_reference")[0];
  }
  const fileCounts = {};
  for (const file of files || []) {
    if (!["image", "images", "images[]", "video", "videos", "videos[]", "audio", "audios", "audios[]", "input_reference"].includes(file.field)) {
      throw new Error("unsupported file field: " + file.field);
    }
    fileCounts[file.field] = (fileCounts[file.field] || 0) + 1;
  }
  if ((fileCounts.input_reference || 0) + (result.input_reference ? 1 : 0) > 1) throw new Error("input_reference requires exactly one image");
  const hasVideo = !!(result.videos && result.videos.length) || (files || []).some(function (file) { return /^videos?(\[\])?$/.test(file.field); });
  if (req._v6kSmartDuration !== undefined && req._v6kSmartDuration !== true) throw new Error("invalid smart duration marker");
  let seconds = req._v6kSmartDuration ? -1 : undefined;
  for (const key of DURATION_FIELDS) {
    if (req[key] === undefined) continue;
    const raw = req[key];
    if ((typeof raw !== "number" && typeof raw !== "string") || !/^(?:-1|[1-9][0-9]{0,3})$/.test(String(raw))) {
      throw new Error("seconds must be -1 or an integer between 1 and 3600");
    }
    const value = Number(raw);
    if (value > MAX_SECONDS || (seconds !== undefined && seconds !== value)) throw new Error("duration fields must agree and not exceed 3600");
    seconds = value;
  }
  // Reference-video edits follow the input length. Do not retry a failed paid task automatically.
  if (hasVideo) seconds = -1;
  if (seconds === -1 && !hasVideo) throw new Error("seconds=-1 requires a reference video");
  if (seconds !== undefined) result.seconds = seconds;
  for (const key of ["ratio", "aspect_ratio"]) {
    if (req[key] === undefined) continue;
    const ratio = trimmed(req[key]);
    if (!/^[1-9][0-9]{0,3}:[1-9][0-9]{0,3}$/.test(ratio)) throw new Error("ratio must be an aspect ratio such as 16:9");
    if (result.ratio && result.ratio !== ratio) throw new Error("ratio and aspect_ratio must match");
    result.ratio = ratio;
  }
  if (req.size !== undefined) {
    if (!/^[1-9][0-9]{0,4}x[1-9][0-9]{0,4}$/.test(trimmed(req.size))) throw new Error("size must be pixel dimensions such as 1280x720, not an aspect ratio");
    result.size = trimmed(req.size);
    if (result.ratio) {
      const dimensions = result.size.split("x").map(Number);
      const ratio = result.ratio.split(":").map(Number);
      if (dimensions[0] * ratio[1] !== dimensions[1] * ratio[0]) throw new Error("size and ratio must match");
    }
  }
  if (req.resolution !== undefined) {
    if (!trimmed(req.resolution)) throw new Error("resolution must be a non-empty string");
    result.resolution = trimmed(req.resolution);
  }
  return result;
}

export function buildSubmitRequest(ctx) {
  const body = normalizedRequest(ctx.requestBody, ctx.upstreamModel, ctx.files);
  const headers = { Authorization: ctx.authHeader || "Bearer " + ctx.apiKey, Accept: "application/json" };
  if ((ctx.files || []).length) {
    const parts = Object.keys(body).map(function (key) {
      return { name: key, value: typeof body[key] === "object" ? JSON.stringify(body[key]) : String(body[key]) };
    });
    for (const file of ctx.files) parts.push({ name: file.field, fileRef: file.ref, filename: file.filename });
    return { url: videoURL(ctx.baseUrl), method: "POST", headers: headers, bodyType: "multipart", parts: parts };
  }
  headers["Content-Type"] = "application/json";
  return { url: videoURL(ctx.baseUrl), method: "POST", headers: headers, body: body };
}

export function parseSubmitResponse(_ctx, resp) {
  const body = resp.body || {};
  if (!trimmed(body.id)) throw new Error(trimmed(body.error && body.error.message) || "provider response did not contain a task id");
  return { taskId: body.id, taskData: body };
}

export function buildQueryRequest(ctx) {
  return { url: videoURL(ctx.baseUrl) + "/" + encodeURIComponent(ctx.taskId), method: "GET", headers: { Authorization: ctx.authHeader || "Bearer " + ctx.apiKey, Accept: "application/json" } };
}

export function parseTaskResult(_ctx, body) {
  body = body || {};
  const statuses = { queued: "QUEUED", in_progress: "IN_PROGRESS", completed: "SUCCESS", failed: "FAILURE" };
  const result = { status: statuses[body.status] || "UNKNOWN" };
  if (typeof body.progress === "number" && body.progress >= 0 && body.progress <= 100) result.progress = body.progress + "%";
  if (result.status === "SUCCESS") result.progress = "100%";
  if (result.status === "FAILURE") {
    const error = body.error || {};
    result.reason = [trimmed(error.code), trimmed(error.message)].filter(Boolean).join(": ") || "video generation failed";
  }
  return result;
}

export function extractUsage(ctx) {
  const body = normalizedRequest(ctx.requestBody, ctx.upstreamModel, ctx.files);
  const hasVideo = !!(body.videos && body.videos.length) || (ctx.files || []).some(function (file) { return /^videos?(\[\])?$/.test(file.field); });
  if (ctx.usagePurpose === "billing_ratios") return null;
  const facts = { video_input: hasVideo ? "video" : "none" };
  const billingModel = meta.models.includes(ctx.upstreamModel) ? ctx.upstreamModel : ctx.model || body.model;
  if (SECOND_MODELS.includes(billingModel)) {
    let seconds = body.seconds;
    const uploadedVideo = (ctx.files || []).some(function (file) { return /^videos?(\[\])?$/.test(file.field); });
    if (seconds === -1 && !uploadedVideo && body.videos && body.videos.length === 1) seconds = body.videos[0].duration_seconds;
    if (typeof seconds !== "number" || !Number.isFinite(seconds) || seconds <= 0 || seconds > MAX_SECONDS) {
      throw new Error("per-second models require seconds; smart duration requires exactly one videos item with positive duration_seconds");
    }
    facts.video_seconds = seconds;
  } else facts.video_count = 1;
  return facts;
}

export function extractUsageOnComplete() { return null; }

export function listArtifacts(task) {
  return task.status === "SUCCESS" ? [{ key: "video", type: "video" }] : [];
}

export function buildContentRequest(ctx) {
  if (ctx.artifactKey !== "video") throw new Error("artifact_not_found");
  const headers = { Authorization: ctx.authHeader || "Bearer " + ctx.apiKey };
  for (const key of Object.keys(ctx.clientRequest.headers || {})) {
    if (["range", "if-range"].includes(key.toLowerCase())) headers[key] = ctx.clientRequest.headers[key];
  }
  // Use the fixed same-origin endpoint; never send the channel key to a result URL.
  return { url: videoURL(ctx.baseUrl) + "/" + encodeURIComponent(ctx.upstreamTaskId) + "/content", method: ctx.clientRequest.method, headers: headers };
}

function decodeVideo(ctx) {
  const body = ctx.body || {};
  let req;
  if (body.kind === "json") req = body.value;
  else if (body.kind === "multipart" || body.kind === "form") {
    req = {};
    for (const key of Object.keys(body.fields || {})) {
      const values = body.fields[key];
      const canonical = key.replace(/\[\]$/, "");
      if (["images", "videos", "audios", "reference_image_urls", "reference_video_urls", "reference_audio_urls"].includes(canonical)) {
        if (Object.prototype.hasOwnProperty.call(req, canonical)) throw new Error("duplicate media field: " + canonical);
        req[canonical] = values.length === 1 && /^\s*\[/.test(values[0]) ? JSON.parse(values[0]) : values;
      } else {
        if (values.length !== 1) throw new Error(key + " must be provided once");
        req[key] = ["metadata", "references"].includes(key) ? JSON.parse(values[0]) : values[0];
      }
    }
  } else throw new Error("JSON or multipart body required");
  const requestBody = normalizedRequest(req, ctx.model || (req && req.model), body.files);
  // Host canonical duration validation stays non-negative. Translate only at the wire boundary.
  if (requestBody.seconds === -1) {
    delete requestBody.seconds;
    requestBody._v6kSmartDuration = true;
  }
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
    render: function (_ctx, task) {
      // Host owns public IDs, status and model. Keep private result URLs out of the response.
      return task.status === "FAILURE" ? { error: { code: "task_failed", message: task.fail_reason || "video generation failed" } } : {};
    },
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
        const content = item.content === undefined ? [item] : Array.isArray(item.content) ? item.content : [item.content];
        for (const part of content) {
          if (typeof part === "string") texts.push(part);
          else if (part && typeof part === "object") {
            if (["input_text", "text"].includes(part.type) && typeof part.text === "string") texts.push(part.text);
            if (["input_image", "image_url"].includes(part.type)) images.push(part.image_url && typeof part.image_url === "object" ? part.image_url.url : part.image_url);
          }
        }
      }
      const request = Object.assign({}, req, { prompt: texts.join("\n").trim() || req.prompt });
      if (images.length) request.images = mediaList(req.images, "images").concat(images);
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
    renderFinal: function (ctx) {
      return { output: [{ type: "message", status: "completed", role: "assistant", content: [{ type: "output_text", text: responsesVideoText(ctx), annotations: [], logprobs: [] }] }], metadata: { vendor: "v6k" } };
    },
  },
};