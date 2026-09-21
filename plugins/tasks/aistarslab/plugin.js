const MODELS = [
  "seedance-720p-c49",
  "seedance-720p-c50",
  "seedance-720p-fast-c49",
  "seedance-720p-fast-c50",
  "seedance-720p-fast-c58",
  "seedance-1080p-c47",
  "seedance-1080p-c48",
  "seedance-1080p-c53",
  "seedance-1080p-c64",
  "seedance-1080p-seedance-2.5-c54",
  "seedance-480p-c47",
  "seedance-480p-c48",
  "seedance-480p-c53",
  "seedance-480p-c64",
  "seedance-480p-fast-c47",
  "seedance-480p-fast-c48",
  "seedance-480p-fast-c53",
  "seedance-480p-fast-c64",
  "seedance-480p-seedance-2.5-c54",
  "seedance-480p-seedance-2.5-c63",
  "seedance-4k-c47",
  "seedance-4k-c48",
  "seedance-4k-c64",
  "seedance-720p-c47",
  "seedance-720p-c48",
  "seedance-720p-c53",
  "seedance-720p-c64",
  "seedance-720p-fast-c47",
  "seedance-720p-fast-c48",
  "seedance-720p-fast-c53",
  "seedance-720p-fast-c64",
  "seedance-720p-seedance-2.5-c54",
  "seedance-720p-seedance-2.5-c63",
];

const USAGE_SCHEMA = {
  seconds: {
    type: "number",
    unit: "second",
    description: { en: "Generated video duration", zh: "生成视频时长" },
  },
  videos: {
    type: "number",
    unit: "count",
    description: { en: "Generated video count", zh: "生成视频数量" },
  },
};

export const meta = {
  apiVersion: 1,
  key: "aistarslab",
  name: "AistarsLab Video",
  icon: "Jimeng.Color",
  description: {
    en: "AistarsLab Seedance video generation",
    zh: "AistarsLab Seedance 视频生成",
  },
  version: "1.0.0",
  author: { name: "Carmin" },
  models: MODELS,
  fetchMode: "per_task",
  usageSchema: USAGE_SCHEMA,
  usageExamples: [
    { label: "5s video", facts: { seconds: 5, videos: 1 } },
    { label: "10s video", facts: { seconds: 10, videos: 1 } },
  ],
  protocols: [{ name: "openai_responses", supports: ["stream", "sync", "background"] }, "openai_video"],
};

function trimmed(value) {
  return String(value || "").trim();
}

function integer(value, fallback) {
  if (value === undefined || value === null || value === "") return fallback;
  const parsed = Number(value);
  if (!Number.isInteger(parsed)) throw new Error("seconds must be a positive integer");
  return parsed;
}

function uniqueURLs(values, kind) {
  const result = [];
  for (const raw of values || []) {
    if (raw && typeof raw === "object" && !Array.isArray(raw) && raw.__fileRef) {
      result.push(raw);
      continue;
    }
    const value = trimmed(raw);
    if (!value || result.includes(value)) continue;
    if (new RegExp("^data:" + kind + "/", "i").test(value)) {
      result.push({ __dataUrl: value, encoding: "publicUrl", mediaKind: kind });
      continue;
    }
    if (!/^https?:\/\//i.test(value)) {
      throw new Error("reference " + kind + " must be an http(s) URL");
    }
    result.push(value);
  }
  return result;
}

function mediaFromContent(metadata) {
  const result = { images: [], videos: [], audios: [] };
  for (const item of Array.isArray(metadata.content) ? metadata.content : []) {
    if (!item || typeof item !== "object" || Array.isArray(item)) continue;
    const mapping = {
      image_url: ["images", "image_url"],
      video_url: ["videos", "video_url"],
      audio_url: ["audios", "audio_url"],
    };
    const target = mapping[item.type];
    if (!target) continue;
    const media = item[target[1]];
    const value = media && typeof media === "object" ? media.url : "";
    if (trimmed(value)) result[target[0]].push(trimmed(value));
  }
  return result;
}

function normalizeMetadata(req) {
  const source = req.metadata || {};
  if (!source || typeof source !== "object" || Array.isArray(source)) throw new Error("metadata must be an object");
  const contentMedia = mediaFromContent(source);
  const images = uniqueURLs([req.image].concat(req.images || [], source.images || [], contentMedia.images), "image");
  const videos = uniqueURLs([].concat(req.videos || [], source.videos || [], contentMedia.videos), "video");
  const audios = uniqueURLs([].concat(req.audios || [], source.audios || [], contentMedia.audios), "audio");
  return {
    resolution: trimmed(req.resolution) || trimmed(source.resolution),
    size: trimmed(source.size),
    ratio: trimmed(source.ratio),
    mode_type: trimmed(req.mode_type) || trimmed(source.mode_type),
    images: images,
    videos: videos,
    audios: audios,
  };
}

function staticCapability(model) {
  const name = trimmed(model).toLowerCase();
  const match = name.match(/-c(\d+)$/);
  if (!name.startsWith("seedance-") || !match) return null;
  const channel = match[1];
  const capability = {
    strict: ["47", "48", "49", "50"].includes(channel),
    minSeconds: 4,
    maxSeconds: 15,
    maxImages: 9,
    maxVideos: 3,
    maxAudios: 3,
    modes: ["text2video", "image2video", "frames2video"],
    ratios: ["16:9", "9:16", "1:1", "4:3", "3:4", "21:9", "2:3", "3:2"],
    resolution: "",
  };
  if (channel === "50") {
    capability.minSeconds = 5;
    capability.maxImages = 4;
    capability.maxAudios = 1;
    capability.modes = ["text2video", "image2video"];
    capability.ratios = ["16:9", "9:16", "1:1"];
    capability.resolution = "720p";
    return capability;
  }
  if (channel === "49") {
    capability.ratios = ["16:9", "9:16", "1:1"];
    capability.resolution = "720p";
    return capability;
  }
  if (name.includes("1080p")) capability.resolution = "1080p";
  else if (name.includes("480p")) capability.resolution = "480p";
  else if (name.includes("720p")) capability.resolution = "720p";
  else if (name.includes("4k")) capability.resolution = "4K";
  return capability;
}

function normalizedRequest(req, upstreamModel) {
  if (!req || typeof req !== "object" || Array.isArray(req)) throw new Error("request body must be an object");
  const model = trimmed(upstreamModel || req.model);
  if (!model) throw new Error("model is required");
  const prompt = trimmed(req.prompt);
  const n = integer(req.n, 1);
  if (n !== 1) throw new Error("only n=1 is supported");
  const metadata = normalizeMetadata(req);
  const capability = staticCapability(req.model || model);
  let seconds = integer(req.seconds !== undefined ? req.seconds : req.duration, capability ? capability.minSeconds : 4);
  if (seconds <= 0) throw new Error("seconds must be a positive integer");
  let mode = metadata.mode_type;
  if (!mode) mode = metadata.images.length ? "image2video" : "text2video";
  let resolution = metadata.resolution;
  if (!resolution && capability) resolution = capability.resolution;
  if (/^4k$/i.test(resolution)) resolution = "4K";
  let size = trimmed(req.size) || metadata.size || metadata.ratio || "16:9";

  if (capability && capability.strict) {
    if (seconds < capability.minSeconds || seconds > capability.maxSeconds) {
      throw new Error("seconds must be between " + capability.minSeconds + " and " + capability.maxSeconds + " for model " + req.model);
    }
    if (metadata.images.length > capability.maxImages || metadata.videos.length > capability.maxVideos || metadata.audios.length > capability.maxAudios) {
      throw new Error(
        "input limits exceeded: images<=" + capability.maxImages + ", videos<=" + capability.maxVideos + ", audios<=" + capability.maxAudios
      );
    }
    if (!capability.modes.includes(mode)) throw new Error("mode is not supported by the selected model");
    if (!capability.ratios.includes(size)) throw new Error("aspect ratio is not supported by the selected model");
    if (capability.resolution && resolution.toLowerCase() !== capability.resolution.toLowerCase()) {
      throw new Error("resolution is not supported by the selected model");
    }
  }
  if (mode === "text2video" && metadata.images.length) throw new Error("text2video does not accept reference images");
  if (mode === "image2video" && !metadata.images.length) throw new Error("image2video requires at least one image");
  if (mode === "frames2video" && metadata.images.length !== 2) throw new Error("frames2video requires exactly two images in first/last order");
  if ([...prompt].length > 2000) throw new Error("prompt must be at most 2000 characters");

  return {
    model: model,
    prompt: prompt,
    seconds: String(seconds),
    size: size,
    n: 1,
    metadata: {
      resolution: resolution,
      mode_type: mode,
      images: metadata.images,
      videos: metadata.videos,
      audios: metadata.audios,
    },
  };
}

function providerVideosURL(baseURL) {
  const base = trimmed(baseURL).replace(/\/+$/, "");
  return /\/v1$/i.test(base) ? base + "/videos" : base + "/v1/videos";
}

export function buildSubmitRequest(ctx) {
  const body = normalizedRequest(ctx.requestBody, ctx.upstreamModel);
  return {
    url: providerVideosURL(ctx.baseUrl),
    method: "POST",
    headers: { Authorization: "Bearer " + ctx.apiKey, Accept: "application/json", "Content-Type": "application/json" },
    body: body,
  };
}

export function parseSubmitResponse(_ctx, resp) {
  const body = resp.body || {};
  if (!body.id) {
    const message = body.error && body.error.message ? body.error.message : "provider response did not contain a task id";
    throw new Error(message);
  }
  return { taskId: String(body.id), taskData: body };
}

export function buildQueryRequest(ctx) {
  return {
    url: providerVideosURL(ctx.baseUrl) + "/" + encodeURIComponent(ctx.taskId),
    method: "GET",
    headers: { Authorization: "Bearer " + ctx.apiKey, Accept: "application/json" },
  };
}

function resultURL(body) {
  const metadata = (body && body.metadata) || {};
  return trimmed(metadata.result_url || body.result_url || body.url);
}

export function parseTaskResult(_ctx, body) {
  const statusMap = {
    queued: "SUBMITTED",
    pending: "SUBMITTED",
    in_progress: "IN_PROGRESS",
    processing: "IN_PROGRESS",
    running: "IN_PROGRESS",
    completed: "SUCCESS",
    succeeded: "SUCCESS",
    success: "SUCCESS",
    done: "SUCCESS",
    failed: "FAILURE",
    cancelled: "FAILURE",
    canceled: "FAILURE",
    expired: "FAILURE",
  };
  const rawStatus = trimmed(body && body.status).toLowerCase();
  let status = statusMap[rawStatus];
  if (!status) status = body && body.error && body.error.message ? "FAILURE" : "IN_PROGRESS";
  const result = { status: status };
  if (body && Number(body.progress) > 0 && Number(body.progress) < 100) result.progress = String(Number(body.progress)) + "%";
  if (status === "SUCCESS") {
    result.progress = "100%";
    const url = resultURL(body);
    if (url) result.url = url;
  }
  if (status === "FAILURE") result.reason = trimmed(body && body.error && body.error.message) || rawStatus || "task failed";
  return result;
}

export function extractUsage(ctx) {
  if (ctx.usagePurpose === "billing_ratios") return null;
  const body = normalizedRequest(ctx.requestBody || {}, ctx.upstreamModel);
  return { seconds: Number(body.seconds), videos: 1 };
}

export function extractUsageOnComplete() {
  return null;
}

function artifactVideoURL(ctx) {
  const data = (ctx && ctx.data) || {};
  return resultURL(data.data && typeof data.data === "object" ? data.data : data);
}

export function listArtifacts(task) {
  return task.status === "SUCCESS" && artifactVideoURL(task) ? [{ key: "video", type: "video" }] : [];
}

export function buildContentRequest(ctx) {
  if (ctx.artifactKey !== "video") throw new Error("artifact_not_found");
  const url = artifactVideoURL(ctx);
  if (!url) throw new Error("artifact_not_found");
  return { url: url, method: ctx.clientRequest.method, credentialless: true };
}

function responsesInput(req) {
  const texts = [];
  const images = [];
  const input = req.input;
  if (typeof input === "string") texts.push(input);
  else if (Array.isArray(input)) {
    for (const item of input) {
      if (typeof item === "string") {
        texts.push(item);
        continue;
      }
      if (!item || typeof item !== "object" || Array.isArray(item)) continue;
      const content = item.content === undefined ? [item] : Array.isArray(item.content) ? item.content : [item.content];
      for (const part of content) {
        if (typeof part === "string") texts.push(part);
        else if (part && typeof part === "object" && !Array.isArray(part)) {
          if (["input_text", "text"].includes(part.type) && typeof part.text === "string") texts.push(part.text);
          if (["input_image", "image_url"].includes(part.type)) {
            const image = part.image_url && typeof part.image_url === "object" ? part.image_url.url : part.image_url;
            if (trimmed(image)) images.push(trimmed(image));
          }
        }
      }
    }
  }
  return { prompt: texts.filter(trimmed).join("\n"), images: images };
}

function videoText(ctx) {
  const artifact = ctx && ctx.artifacts && ctx.artifacts.video;
  const url = trimmed(artifact && artifact.url);
  if (!url) throw new Error("video artifact is unavailable");
  const escaped = url.replace(/&/g, "&amp;").replace(/\"/g, "&quot;").replace(/</g, "&lt;").replace(/>/g, "&gt;");
  return '<video controls src="' + escaped + '"></video>';
}

export const protocols = {
  openai_responses: {
    decodeRequest: function (ctx) {
      if (!ctx.body || ctx.body.kind !== "json") throw new Error("JSON body required");
      const req = ctx.body.value;
      if (!req || typeof req !== "object" || Array.isArray(req)) throw new Error("request body must be an object");
      if (req.input !== undefined && typeof req.input !== "string" && !Array.isArray(req.input)) throw new Error("input must be a string or array");
      const input = responsesInput(req);
      const requestBody = Object.assign({}, req, { prompt: input.prompt || trimmed(req.prompt) });
      if (input.images.length) requestBody.images = [].concat(req.images || [], input.images);
      if (Object.prototype.hasOwnProperty.call(req, "seconds")) requestBody.seconds = req.seconds;
      normalizedRequest(requestBody, ctx.upstreamModel || req.model);
      return { kind: "submit", model: req.model, action: "generate", requestBody: requestBody };
    },
    renderEvents: function (ctx, task, previousState) {
      const status = String(task.status || "UNKNOWN").toUpperCase();
      const value = Number(String(task.progress || "").replace("%", ""));
      const progress = Number.isFinite(value) && value >= 0 && value <= 100 ? value : null;
      const state = { status: status, progress: progress };
      if (status === "SUCCESS") {
        const events = previousState && previousState.status === status ? [] : [{ type: "output", data: videoText(ctx) }];
        return { events: events, state: state, done: true };
      }
      if (status === "FAILURE") return { events: [{ type: "error", code: "task_failed", message: task.fail_reason || "task failed" }], state: state, done: true };
      if (previousState && previousState.status === status && previousState.progress === progress) return { events: [], state: state, done: false };
      const event = { type: "progress", message: status.toLowerCase() };
      if (progress !== null) event.progress = progress;
      return { events: [event], state: state, done: false };
    },
    renderFinal: function (ctx) {
      return {
        output: [{ type: "message", status: "completed", role: "assistant", content: [{ type: "output_text", text: videoText(ctx), annotations: [], logprobs: [] }] }],
        metadata: { vendor: "aistarslab" },
      };
    },
  },
  openai_video: {
    decodeRequest: function (ctx) {
      if (!ctx.body || (ctx.body.kind !== "json" && ctx.body.kind !== "multipart")) {
        throw new Error("JSON or multipart body required");
      }
      let req;
      if (ctx.body.kind === "json") {
        if (!ctx.body.value || typeof ctx.body.value !== "object" || Array.isArray(ctx.body.value)) throw new Error("JSON object required");
        req = Object.assign({}, ctx.body.value);
      } else {
        req = {};
        const fields = ctx.body.fields || {};
        const first = function (name) {
          const values = fields[name] || [];
          if (values.length > 1) throw new Error(name + " must be provided once");
          return values[0];
        };
        for (const name of Object.keys(fields)) req[name] = first(name);
        if (req.metadata !== undefined) {
          try {
            req.metadata = JSON.parse(req.metadata);
          } catch (_error) {
            throw new Error("metadata must be a JSON object string");
          }
          if (!req.metadata || typeof req.metadata !== "object" || Array.isArray(req.metadata)) {
            throw new Error("metadata must be a JSON object string");
          }
        }
        if (req.seconds !== undefined) req.seconds = Number(req.seconds);
        if (req.duration !== undefined) req.duration = Number(req.duration);
        if (req.n !== undefined) req.n = Number(req.n);
        const files = ctx.body.files || [];
        if (files.length > 1) throw new Error("input_reference must be provided once");
        if (files.length === 1) {
          if (files[0].field !== "input_reference") throw new Error("unexpected file field: " + files[0].field);
          req.images = [{ __fileRef: files[0].ref, encoding: "publicUrl", mediaKind: "image", maxBytes: 31457280 }];
        }
      }
      const model = ctx.model || req.model;
      normalizedRequest(req, ctx.upstreamModel || model);
      return { kind: "submit", model: model, action: "generate", requestBody: Object.assign({}, req, { model: model }) };
    },
    render: function (_ctx, task) {
      const statusMap = { NOT_START: "queued", SUBMITTED: "queued", QUEUED: "queued", IN_PROGRESS: "in_progress", SUCCESS: "completed", FAILURE: "failed" };
      const output = {
        id: task.task_id,
        task_id: task.task_id,
        object: "video",
        model: "",
        status: statusMap[task.status] || "unknown",
        progress: Number(String(task.progress || "0").replace("%", "")),
        created_at: task.created_at,
      };
      if (task.updated_at) output.completed_at = task.updated_at;
      if (task.status === "FAILURE") output.error = { message: task.fail_reason || "task failed", code: "task_failed" };
      return output;
    },
  },
};
