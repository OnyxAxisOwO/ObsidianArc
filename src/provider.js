// Talks to Anthropic's Messages API or any OpenAI-compatible /chat/completions
// endpoint (DeepSeek, OpenRouter, Groq, a local Ollama/vLLM, and so on), with a
// user-supplied base URL for both. This is the only file in the project that
// makes a network request.
//
// Anthropic's `anthropic-dangerous-direct-browser-access` header is what makes
// calling the API straight from a browser tab possible at all — normally its
// CORS policy refuses a page origin. It is what its name says: dangerous,
// because it puts the user's own API key in front of anything running on the
// page. That trade is the whole premise of a bring-your-own-key tool with no
// server of its own, so it is made once here rather than asked of every host
// that embeds this component.
//
// Loaded as a plain global-scope script (no bundler in this project).

(function (root, factory) {
  const api = factory();
  if (typeof module === 'object' && module.exports) module.exports = api;
  if (root) root.ObsidianProvider = api;
})(typeof globalThis !== 'undefined' ? globalThis : this, function () {
  'use strict';

  const ANTHROPIC_VERSION = '2023-06-01';

  const PROVIDERS = Object.freeze({
    anthropic: Object.freeze({
      id: 'anthropic',
      label: 'Anthropic',
      defaultBaseUrl: 'https://api.anthropic.com',
      defaultModel: 'claude-sonnet-5',
      vision: true,
      models: Object.freeze(['claude-opus-5', 'claude-sonnet-5', 'claude-haiku-4-5'])
    }),
    openai: Object.freeze({
      id: 'openai',
      label: 'OpenAI Compatible',
      defaultBaseUrl: 'https://api.openai.com/v1',
      defaultModel: '',
      vision: false,
      // With an arbitrary base URL the valid ids are unknowable, so this
      // provider deliberately suggests nothing — a stale guess is worse than a
      // blank field the user fills in from their own provider's docs, or the
      // "Detect" button, which asks the endpoint directly.
      models: Object.freeze([])
    })
  });

  const DEFAULT_PROVIDER = 'anthropic';
  const MAX_REPLY_CHARS = 16000;
  const MAX_THINKING_CHARS = 20000;
  const MAX_SYSTEM_PROMPT_CHARS = 4000;
  const MAX_MESSAGE_CHARS = 8000;
  const MAX_EXTRA_BODY_CHARS = 4000;
  // Enough for a long-running conversation while keeping a runaway transcript
  // from being re-sent (and re-billed) in full on every turn.
  const MAX_CHAT_TURNS = 40;
  const MAX_IMAGES_PER_REQUEST = 6;
  const MAX_IMAGE_DATA_CHARS = 2 * 1024 * 1024;
  const IMAGE_DATA_URL_RE = /^data:image\/(?:png|jpeg|webp|gif);base64,[A-Za-z0-9+/]+=*$/;
  const IMAGES_REJECTED_PREFIX = 'This model or endpoint did not accept an attached image.';

  function trimTo(value, limit) {
    return typeof value === 'string' ? value.trim().slice(0, limit) : '';
  }

  // --- config -----------------------------------------------------------

  const MAX_SAVED_MODELS = 40;
  const MAX_MODEL_ID_CHARS = 120;
  const MAX_MODEL_LABEL_CHARS = 60;

  function sanitizeModels(raw) {
    const seen = new Set();
    const models = [];
    for (const entry of (Array.isArray(raw) ? raw : [])) {
      if (!entry || typeof entry !== 'object') continue;
      const id = trimTo(entry.id, MAX_MODEL_ID_CHARS);
      if (!id || seen.has(id)) continue;
      seen.add(id);
      const label = trimTo(entry.label, MAX_MODEL_LABEL_CHARS);
      models.push(label ? { id, label } : { id });
      if (models.length >= MAX_SAVED_MODELS) break;
    }
    return models;
  }

  // The name a model is shown under: its saved label if the user gave it one,
  // the raw id otherwise. One definition so the header chip and the settings
  // list cannot disagree about what a model is called.
  function modelLabel(config, modelId) {
    const id = typeof modelId === 'string' ? modelId.trim() : '';
    if (!id) return '';
    const saved = (config && Array.isArray(config.models) ? config.models : [])
      .find((entry) => entry && entry.id === id);
    return (saved && saved.label) || id;
  }

  function normalizeTemperature(value) {
    const num = typeof value === 'number' ? value : parseFloat(value);
    return Number.isFinite(num) ? Math.min(2, Math.max(0, num)) : null;
  }

  function normalizeMaxTokens(value) {
    const num = typeof value === 'number' ? value : parseInt(value, 10);
    return Number.isFinite(num) && num > 0 ? Math.min(64000, Math.round(num)) : null;
  }

  function normalizeThinkingBudget(value) {
    const num = typeof value === 'number' ? value : parseInt(value, 10);
    return Number.isFinite(num) && num >= 1024 ? Math.min(32000, Math.round(num)) : 4096;
  }

  // The escape hatch for a provider parameter this settings panel has no
  // dedicated control for (top_p, seed, a reasoning-effort flag...). Invalid
  // JSON is treated as empty rather than failing the request.
  function parseExtraBody(raw) {
    if (typeof raw !== 'string' || !raw.trim()) return {};
    try {
      const parsed = JSON.parse(raw);
      return parsed && typeof parsed === 'object' && !Array.isArray(parsed) ? parsed : {};
    } catch (_) {
      return {};
    }
  }

  function extraBodyLooksValid(raw) {
    if (typeof raw !== 'string' || !raw.trim()) return true;
    try {
      const parsed = JSON.parse(raw);
      return !!parsed && typeof parsed === 'object' && !Array.isArray(parsed);
    } catch (_) {
      return false;
    }
  }

  // Accepts a stored config of either shape: a bare {apiKey, model}, or the
  // current multi-field object.
  function normalizeConfig(raw) {
    const source = raw && typeof raw === 'object' ? raw : {};
    const provider = Object.prototype.hasOwnProperty.call(PROVIDERS, source.provider) ? source.provider : DEFAULT_PROVIDER;
    const preset = PROVIDERS[provider];
    const models = sanitizeModels(source.models);
    return {
      provider,
      apiKey: typeof source.apiKey === 'string' ? source.apiKey.trim() : '',
      model: (typeof source.model === 'string' && source.model.trim()) || preset.defaultModel,
      // Falls back to the provider's own curated list when the shortlist is
      // empty, so a fresh install (or a provider switch) has something to
      // pick from instead of an empty menu. OpenAI-compatible starts with no
      // suggestions at all (see PROVIDERS above) and stays that way.
      models: models.length ? models : preset.models.map((id) => ({ id })),
      baseUrl: typeof source.baseUrl === 'string' ? source.baseUrl.trim() : '',
      // Whether the chosen model reads images. Nothing can ask an endpoint
      // this, so it is the user's answer, defaulting to what the provider
      // makes true of every model it offers.
      vision: typeof source.vision === 'boolean' ? source.vision : preset.vision === true,
      streaming: typeof source.streaming === 'boolean' ? source.streaming : true,
      // A standing instruction applied to every conversation — the closest
      // thing this tool has to a persona or a system prompt the user controls.
      systemPrompt: trimTo(source.systemPrompt, MAX_SYSTEM_PROMPT_CHARS),
      temperature: normalizeTemperature(source.temperature),
      maxTokens: normalizeMaxTokens(source.maxTokens),
      extraBody: trimTo(source.extraBody, MAX_EXTRA_BODY_CHARS),
      // Anthropic-only: asks the model to show its work before answering.
      // Requires temperature to be left at the API default, which buildBody
      // enforces by omitting it whenever this is on.
      extendedThinking: typeof source.extendedThinking === 'boolean' ? source.extendedThinking : false,
      thinkingBudget: normalizeThinkingBudget(source.thinkingBudget),
      // Whether the key this object carries is stored at rest as apiKeyEnc
      // (see encryptSecret) instead of the plain apiKey field above. This
      // object itself always carries the plaintext key — the encrypted form
      // only ever exists in the storage adapter, produced and consumed by
      // saveConfig/loadConfig below.
      encryptApiKey: typeof source.encryptApiKey === 'boolean' ? source.encryptApiKey : false
    };
  }

  // --- API key at rest -----------------------------------------------------
  // AES-256-GCM with a key generated on-device and never itself sent anywhere.
  // This defends the everyday accident — a storage dump, a synced backup
  // tool, a screen share of devtools' Application tab — showing the key in
  // the clear. It does not defend against anything that can read this page's
  // own storage, because the unwrap key necessarily lives there too: there is
  // no password or remote secret to derive it from in a one-click toggle.
  const CONFIG_STORAGE_KEY = '__obsidian_arc_ai_config__';
  const KEY_MATERIAL_STORAGE_KEY = '__obsidian_arc_ai_key_material__';

  function bufToBase64(buf) {
    const bytes = new Uint8Array(buf);
    let binary = '';
    for (let i = 0; i < bytes.length; i++) binary += String.fromCharCode(bytes[i]);
    return btoa(binary);
  }

  function base64ToBuf(b64) {
    const binary = atob(b64);
    const bytes = new Uint8Array(binary.length);
    for (let i = 0; i < binary.length; i++) bytes[i] = binary.charCodeAt(i);
    return bytes.buffer;
  }

  async function getDeviceKey(store) {
    const data = await store.get(KEY_MATERIAL_STORAGE_KEY);
    const existing = data && data[KEY_MATERIAL_STORAGE_KEY];
    if (typeof existing === 'string' && existing) {
      return crypto.subtle.importKey('raw', base64ToBuf(existing), { name: 'AES-GCM' }, false, ['encrypt', 'decrypt']);
    }
    const key = await crypto.subtle.generateKey({ name: 'AES-GCM', length: 256 }, true, ['encrypt', 'decrypt']);
    const raw = await crypto.subtle.exportKey('raw', key);
    await store.set({ [KEY_MATERIAL_STORAGE_KEY]: bufToBase64(raw) });
    return key;
  }

  async function encryptSecret(plainText, store) {
    const key = await getDeviceKey(store);
    const iv = crypto.getRandomValues(new Uint8Array(12));
    const cipherBuf = await crypto.subtle.encrypt({ name: 'AES-GCM', iv }, key, new TextEncoder().encode(plainText));
    return { iv: bufToBase64(iv), ct: bufToBase64(cipherBuf) };
  }

  // Never throws: a corrupted blob or a wiped device key degrades to "no key
  // configured" rather than breaking the settings panel or the chat.
  async function decryptSecret(payload, store) {
    if (!payload || typeof payload !== 'object' || typeof payload.iv !== 'string' || typeof payload.ct !== 'string') return '';
    try {
      const key = await getDeviceKey(store);
      const iv = new Uint8Array(base64ToBuf(payload.iv));
      const plainBuf = await crypto.subtle.decrypt({ name: 'AES-GCM', iv }, key, base64ToBuf(payload.ct));
      return new TextDecoder().decode(plainBuf);
    } catch (_) {
      return '';
    }
  }

  // Every caller — the settings panel, the model chip, the chat component —
  // goes through this instead of reading the storage adapter directly, so
  // there is exactly one place that knows apiKeyEnc exists.
  async function loadConfig(storage) {
    if (!storage) return normalizeConfig(null);
    const data = await storage.get(CONFIG_STORAGE_KEY);
    const raw = (data && data[CONFIG_STORAGE_KEY]) || {};
    const apiKey = raw.apiKeyEnc ? await decryptSecret(raw.apiKeyEnc, storage) : raw.apiKey;
    return normalizeConfig({ ...raw, apiKey });
  }

  // Read-modify-write against whatever is currently stored: the config is one
  // object behind one storage key, so saving one field has to start from the
  // latest copy of every other field or it clobbers a change made elsewhere.
  async function saveConfig(partial, storage) {
    if (!storage) throw new Error('No storage available.');
    const current = await loadConfig(storage);
    const merged = normalizeConfig({ ...current, ...partial });
    const record = { ...merged };
    delete record.apiKeyEnc;
    if (merged.encryptApiKey && merged.apiKey) {
      record.apiKeyEnc = await encryptSecret(merged.apiKey, storage);
      record.apiKey = '';
    }
    await storage.set({ [CONFIG_STORAGE_KEY]: record });
    return merged;
  }

  // The base URL is where the user's API key gets sent, so it is validated
  // rather than interpolated as typed. Plain http is allowed only for
  // loopback addresses, which is how a local Ollama or vLLM instance is
  // reached.
  function normalizeBaseUrl(value, fallback) {
    const candidate = typeof value === 'string' ? value.trim() : '';
    if (!candidate) return fallback;
    let url;
    try {
      url = new URL(/^[a-z][a-z\d+.-]*:/i.test(candidate) ? candidate : `https://${candidate}`);
    } catch (_) {
      throw new Error(`Invalid base URL: ${candidate}`);
    }
    const isLoopback = ['localhost', '127.0.0.1', '[::1]', '::1'].includes(url.hostname);
    if (url.protocol !== 'https:' && !(url.protocol === 'http:' && isLoopback)) {
      throw new Error('Base URL must use https (http is allowed only for localhost).');
    }
    if (url.username || url.password) throw new Error('Base URL must not embed credentials.');
    return url.href.replace(/\/+$/, '');
  }

  // Providers document the full endpoint, not a base — so that is what users
  // tend to paste. An already-complete URL is detected and used as-is; every
  // partial form is accepted too, because there is no way to tell a user
  // which of the three they should have entered.
  function resolveEndpoint(provider, base) {
    if (provider === 'anthropic') {
      if (/\/messages$/.test(base)) return base;
      if (/\/v1$/.test(base)) return `${base}/messages`;
      return `${base}/v1/messages`;
    }
    if (/\/chat\/completions$/.test(base)) return base;
    return `${base}/chat/completions`;
  }

  function modelsEndpoint(provider, base) {
    if (provider === 'anthropic') {
      const root = base.replace(/\/v1\/messages$/, '').replace(/\/messages$/, '').replace(/\/v1$/, '');
      return `${root}/v1/models?limit=200`;
    }
    const root = base.replace(/\/chat\/completions$/, '');
    return `${root}/models`;
  }

  function requestHeaders(config) {
    if (config.provider === 'anthropic') {
      return {
        'content-type': 'application/json',
        'x-api-key': config.apiKey,
        'anthropic-version': ANTHROPIC_VERSION,
        'anthropic-dangerous-direct-browser-access': 'true'
      };
    }
    return { 'content-type': 'application/json', authorization: `Bearer ${config.apiKey}` };
  }

  // Asks the configured endpoint what models it offers. Returns [{id, label}]
  // — label only when the provider names one (Anthropic's display_name); the
  // caller decides what to do with the list, nothing is written here.
  async function listModels(config, signal) {
    const clean = normalizeConfig(config);
    if (!clean.apiKey) throw new Error('No API key configured.');
    const preset = PROVIDERS[clean.provider];
    const base = normalizeBaseUrl(clean.baseUrl, preset.defaultBaseUrl);
    const url = modelsEndpoint(clean.provider, base);
    const headers = requestHeaders(clean);
    delete headers['content-type'];

    let response;
    try {
      response = await fetch(url, { method: 'GET', signal, headers });
    } catch (error) {
      throw new Error(`Network request failed: ${(error && error.message) || error}`);
    }
    let payload;
    try {
      payload = await response.json();
    } catch (_) {
      throw new Error(`The endpoint answered with something other than JSON (HTTP ${response.status}).`);
    }
    if (!response.ok) {
      const detail = payload && payload.error && (payload.error.message || payload.error);
      const message = typeof detail === 'string' && detail ? detail : `Listing models failed with HTTP ${response.status}.`;
      throw new Error(response.status === 401 || response.status === 403
        ? `${message} (sent as ${clean.provider} to ${url})`
        : message);
    }
    const list = Array.isArray(payload.data) ? payload.data : (Array.isArray(payload.models) ? payload.models : []);
    return list
      .map((entry) => {
        const id = trimTo(entry && (entry.id || entry.name), MAX_MODEL_ID_CHARS);
        if (!id) return null;
        const label = trimTo(entry && entry.display_name, MAX_MODEL_LABEL_CHARS);
        return label && label !== id ? { id, label } : { id };
      })
      .filter(Boolean);
  }

  // --- building a request -----------------------------------------------

  function readyConfig(config) {
    const clean = normalizeConfig(config);
    if (!clean.apiKey) throw new Error('No API key configured.');
    if (!clean.model) throw new Error('No model configured.');
    return clean;
  }

  function capTurns(turns) {
    const list = (Array.isArray(turns) ? turns : []).filter((turn) => turn && (turn.role === 'user' || turn.role === 'assistant'));
    return list.length <= MAX_CHAT_TURNS ? list : list.slice(-MAX_CHAT_TURNS);
  }

  function toImageBlock(provider, dataUrl) {
    if (provider !== 'anthropic') return { type: 'image_url', image_url: { url: dataUrl } };
    return {
      type: 'image',
      source: {
        type: 'base64',
        media_type: dataUrl.slice('data:'.length, dataUrl.indexOf(';')),
        data: dataUrl.slice(dataUrl.indexOf(',') + 1)
      }
    };
  }

  // Anthropic rejects a message list that does not start with a user turn,
  // and both providers treat two consecutive same-role messages as malformed.
  // Editing a message mid-transcript can produce either, so the list is
  // repaired here rather than trusted.
  function mergeAdjacent(messages) {
    const merged = [];
    for (const message of messages) {
      const last = merged[merged.length - 1];
      if (!last || last.role !== message.role) {
        merged.push({ ...message });
        continue;
      }
      if (typeof last.content === 'string' && typeof message.content === 'string') {
        last.content = `${last.content}\n\n${message.content}`;
      } else {
        const toBlocks = (content) => (Array.isArray(content) ? content : [{ type: 'text', text: content }]);
        last.content = toBlocks(last.content).concat(toBlocks(message.content));
      }
    }
    while (merged.length && merged[0].role !== 'user') merged.shift();
    return merged;
  }

  function buildMessages(config, turns) {
    const list = capTurns(turns);
    // Numbered once per request, image identity determined by data URL, so a
    // picture attached three turns ago and referenced again still reads as
    // one picture rather than being sent (and billed) twice.
    const seen = new Map();
    let imageCount = 0;

    const messages = list.map((turn) => {
      if (turn.role === 'assistant') return { role: 'assistant', content: trimTo(turn.content, MAX_REPLY_CHARS) };

      const text = trimTo(turn.content, MAX_MESSAGE_CHARS);
      const blocks = [];
      for (const image of (config.vision === false ? [] : (Array.isArray(turn.images) ? turn.images : []))) {
        const dataUrl = image && typeof image.dataUrl === 'string' ? image.dataUrl.trim() : '';
        if (!dataUrl || dataUrl.length > MAX_IMAGE_DATA_CHARS || !IMAGE_DATA_URL_RE.test(dataUrl)) continue;
        if (!seen.has(dataUrl)) {
          if (imageCount >= MAX_IMAGES_PER_REQUEST) continue;
          imageCount += 1;
          seen.set(dataUrl, imageCount);
        }
        blocks.push(toImageBlock(config.provider, dataUrl));
      }
      if (!blocks.length) return { role: 'user', content: text };
      const content = text ? [{ type: 'text', text }, ...blocks] : blocks;
      return { role: 'user', content };
    });

    return mergeAdjacent(messages);
  }

  function buildBody(config, messages) {
    const body = { model: config.model, messages };
    const useThinking = config.provider === 'anthropic' && config.extendedThinking;
    body.max_tokens = config.maxTokens || (useThinking ? Math.max(config.thinkingBudget + 1024, 4096) : 4096);
    // Anthropic requires temperature to be left at its default (or set to 1)
    // whenever extended thinking is on; everywhere else the user's own value
    // (if any) is honoured.
    if (config.temperature !== null && !useThinking) body.temperature = config.temperature;
    if (useThinking) body.thinking = { type: 'enabled', budget_tokens: config.thinkingBudget };

    if (config.provider === 'anthropic') {
      if (config.systemPrompt) body.system = config.systemPrompt;
    } else if (config.systemPrompt) {
      body.messages = [{ role: 'system', content: config.systemPrompt }, ...messages];
    }

    return { ...body, ...parseExtraBody(config.extraBody) };
  }

  function buildRequest(config, turns) {
    const preset = PROVIDERS[config.provider];
    const base = normalizeBaseUrl(config.baseUrl, preset.defaultBaseUrl);
    return {
      url: resolveEndpoint(config.provider, base),
      headers: requestHeaders(config),
      body: buildBody(config, buildMessages(config, turns))
    };
  }

  function carriesImages(body) {
    const messages = Array.isArray(body && body.messages) ? body.messages : [];
    return messages.some((message) => Array.isArray(message && message.content) && message.content
      .some((block) => block && (block.type === 'image' || block.type === 'image_url')));
  }

  function extractReply(provider, payload) {
    if (provider === 'anthropic') {
      if (payload.stop_reason === 'refusal') throw new Error('The model declined to answer.');
      const block = Array.isArray(payload.content) ? payload.content.find((entry) => entry && entry.type === 'text') : null;
      const thinkingBlock = Array.isArray(payload.content) ? payload.content.find((entry) => entry && entry.type === 'thinking') : null;
      if (!block || typeof block.text !== 'string') throw new Error('API response contained no text block.');
      return { reply: block.text, thinking: (thinkingBlock && thinkingBlock.thinking) || '' };
    }
    const choice = Array.isArray(payload.choices) ? payload.choices[0] : null;
    const message = choice && choice.message;
    if (message && message.refusal) throw new Error(String(message.refusal));
    if (!message || typeof message.content !== 'string') throw new Error('API response contained no message content.');
    return { reply: message.content, thinking: (typeof message.reasoning_content === 'string' && message.reasoning_content)
      || (typeof message.reasoning === 'string' && message.reasoning) || '' };
  }

  function pickUsage(provider, usage) {
    if (!usage || typeof usage !== 'object') return null;
    const count = (value) => (Number.isFinite(value) && value >= 0 ? Math.round(value) : null);
    const input = count(provider === 'anthropic' ? usage.input_tokens : usage.prompt_tokens);
    const output = count(provider === 'anthropic' ? usage.output_tokens : usage.completion_tokens);
    if (input === null && output === null) return null;
    const result = {};
    if (input !== null) result.inputTokens = input;
    if (output !== null) result.outputTokens = output;
    return result;
  }

  function turnStats({ startedAt, firstTokenAt, streamed, usage }) {
    const ms = Math.max(0, Date.now() - startedAt);
    const firstTokenMs = firstTokenAt ? Math.max(0, firstTokenAt - startedAt) : null;
    const outputTokens = usage && Number.isFinite(usage.outputTokens) ? usage.outputTokens : null;
    const window = firstTokenMs === null ? ms : ms - firstTokenMs;
    const generatingMs = window >= 250 ? window : ms;
    const tps = outputTokens !== null && generatingMs > 0 ? Math.round((outputTokens / (generatingMs / 1000)) * 10) / 10 : null;
    return {
      ms,
      firstTokenMs,
      streamed: !!streamed,
      inputTokens: usage && Number.isFinite(usage.inputTokens) ? usage.inputTokens : null,
      outputTokens,
      tps
    };
  }

  async function postJson(url, headers, body, signal) {
    let response;
    try {
      response = await fetch(url, { method: 'POST', signal, headers, body: JSON.stringify(body) });
    } catch (error) {
      if (signal && signal.aborted) throw error;
      throw new Error(`Network request failed: ${(error && error.message) || error}`);
    }
    let payload;
    try {
      payload = await response.json();
    } catch (_) {
      throw new Error(`API returned a non-JSON response (HTTP ${response.status}).`);
    }
    return { response, payload };
  }

  function explainFailure(config, request, response, payload) {
    const detail = payload && payload.error && (payload.error.message || payload.error);
    const message = typeof detail === 'string' && detail ? detail : `API request failed with HTTP ${response.status}.`;
    // An auth failure is almost never "the key has a typo" — it is usually a
    // key issued for a different service than the selected provider, or a
    // base URL pointing somewhere that wants a different credential.
    if (response.status === 401 || response.status === 403) {
      return new Error(`${message} (sent as ${config.provider} to ${request.url})`);
    }
    // Text-only models are the common case behind an OpenAI-compatible base
    // URL, and their rejection names the wire format rather than the cause.
    if (carriesImages(request.body) && [400, 415, 422].includes(response.status)) {
      return new Error(`${IMAGES_REJECTED_PREFIX} ${message}`);
    }
    return new Error(message);
  }

  async function sendRequest(config, request, signal, meta) {
    const { response, payload } = await postJson(request.url, request.headers, request.body, signal);
    if (!response.ok) throw explainFailure(config, request, response, payload);
    if (meta) meta.usage = pickUsage(config.provider, payload.usage);
    return extractReply(config.provider, payload);
  }

  // --- streaming -----------------------------------------------------------

  const STREAM_UNSUPPORTED = 'obsidian-arc:stream-unsupported';

  function streamUnsupported(reason) {
    const error = new Error(STREAM_UNSUPPORTED);
    error.streamReason = reason || '';
    return error;
  }

  function streamUsage(provider, event) {
    if (provider === 'anthropic') {
      if (event && event.type === 'message_start') return pickUsage(provider, event.message && event.message.usage);
      return pickUsage(provider, event && event.usage);
    }
    return pickUsage(provider, (event && event.usage) || (event && event.x_groq && event.x_groq.usage));
  }

  function streamDelta(provider, event) {
    if (provider === 'anthropic') {
      const delta = event && event.delta;
      if (delta && delta.type && delta.type !== 'text_delta') return '';
      return delta && typeof delta.text === 'string' ? delta.text : '';
    }
    const choice = event && Array.isArray(event.choices) ? event.choices[0] : null;
    const delta = choice && choice.delta;
    return delta && typeof delta.content === 'string' ? delta.content : '';
  }

  // The model's own reasoning. Two shapes exist: a field of its own
  // (Anthropic's thinking deltas, and the reasoning/reasoning_content
  // OpenAI-compatible servers have settled on), or `<think>...</think>`
  // inline at the head of the content, which is what some reasoning models
  // emit on an OpenAI-compatible endpoint. Both are handled below.
  function streamReasoning(provider, event) {
    if (provider === 'anthropic') {
      const delta = event && event.delta;
      return delta && delta.type === 'thinking_delta' && typeof delta.thinking === 'string' ? delta.thinking : '';
    }
    const choice = event && Array.isArray(event.choices) ? event.choices[0] : null;
    const delta = choice && choice.delta;
    if (!delta) return '';
    if (typeof delta.reasoning_content === 'string') return delta.reasoning_content;
    return typeof delta.reasoning === 'string' ? delta.reasoning : '';
  }

  const THINK_OPEN = '<think>';
  const THINK_CLOSE = '</think>';

  // Splits inline reasoning off the front of an answer that may stop
  // anywhere, including inside the tag. Everything after an unclosed
  // `<think>` is reasoning: the answer has not started yet.
  function splitThinking(buffer) {
    const text = String(buffer || '');
    const open = text.indexOf(THINK_OPEN);
    if (open === -1) return { thinking: '', body: text };
    const close = text.indexOf(THINK_CLOSE, open + THINK_OPEN.length);
    if (close === -1) return { thinking: text.slice(open + THINK_OPEN.length), body: text.slice(0, open) };
    return {
      thinking: text.slice(open + THINK_OPEN.length, close),
      body: text.slice(0, open) + text.slice(close + THINK_CLOSE.length)
    };
  }

  // Server-sent events, framed by blank lines. Hand-rolled because the two
  // providers only ever use the `data:` field of the format.
  async function readEventStream(response, provider, onText) {
    const reader = response.body.getReader();
    const decoder = new TextDecoder();
    let pending = '';
    let raw = '';
    let reasoning = '';
    let usage = null;

    for (;;) {
      const { value, done } = await reader.read();
      if (done) break;
      pending += decoder.decode(value, { stream: true });

      // \r\n\r\n as well as \n\n: some proxies rewrite the line endings.
      let match = /\r?\n\r?\n/.exec(pending);
      while (match) {
        const chunk = pending.slice(0, match.index);
        pending = pending.slice(match.index + match[0].length);
        match = /\r?\n\r?\n/.exec(pending);

        for (const line of chunk.split(/\r?\n/)) {
          if (!line.startsWith('data:')) continue;
          const data = line.slice(5).trim();
          if (!data || data === '[DONE]') continue;
          let event;
          try {
            event = JSON.parse(data);
          } catch (_) {
            continue;
          }
          if (event && event.type === 'error') {
            throw new Error((event.error && event.error.message) || 'The API reported an error mid-stream.');
          }
          const counts = streamUsage(provider, event);
          if (counts) usage = { ...(usage || {}), ...counts };
          const thought = streamReasoning(provider, event);
          const text = streamDelta(provider, event);
          if (!thought && !text) continue;
          reasoning += thought;
          raw += text;
          onText(raw, reasoning);
        }
      }
    }
    return { raw, reasoning, usage };
  }

  async function sendStreamingRequest(config, request, signal, onReply, onThinking) {
    const body = { ...request.body, stream: true };
    if (config.provider !== 'anthropic') body.stream_options = { include_usage: true };
    let response;
    try {
      response = await fetch(request.url, { method: 'POST', signal, headers: request.headers, body: JSON.stringify(body) });
    } catch (error) {
      if (signal && signal.aborted) throw error;
      throw new Error(`Network request failed: ${(error && error.message) || error}`);
    }

    const contentType = (response.headers.get('content-type') || '').toLowerCase();
    if (!response.ok || !response.body || !contentType.includes('text/event-stream')) {
      throw streamUnsupported(response.ok
        ? `the endpoint answered with ${contentType || 'no content type'} instead of an event stream`
        : `the endpoint answered HTTP ${response.status}`);
    }

    let lastReply = '';
    let lastThinking = '';
    const { raw, reasoning, usage } = await readEventStream(response, config.provider, (buffer, reasoned) => {
      const split = splitThinking(buffer);
      const thinking = reasoned + split.thinking;
      if (thinking !== lastThinking) {
        lastThinking = thinking;
        onThinking(thinking);
      }
      if (split.body === lastReply) return;
      lastReply = split.body;
      onReply(split.body);
    });

    const answer = splitThinking(raw);
    if (!answer.body.trim() && !reasoning && !answer.thinking) throw streamUnsupported('the event stream carried no text');
    return { reply: answer.body, thinking: reasoning + answer.thinking, usage };
  }

  // One turn of the conversation. `turns` is the whole visible transcript,
  // including the message just typed — the API is stateless, so the caller
  // owning the history is what makes editing an earlier message (and
  // re-running from there) a matter of truncating an array rather than of
  // server state.
  async function chat({ config, turns, signal }) {
    const clean = readyConfig(config);
    const request = buildRequest(clean, turns);
    const meta = {};
    const { reply, thinking } = await sendRequest(clean, request, signal, meta);
    return {
      reply: trimTo(reply, MAX_REPLY_CHARS),
      thinking: trimTo(thinking, MAX_THINKING_CHARS),
      stats: turnStats({ startedAt: Date.now(), firstTokenAt: 0, streamed: false, usage: meta.usage })
    };
  }

  // Same contract as chat() above, plus onReply/onThinking callbacks that fire
  // as the answer arrives. Falls back to the one-shot path — and therefore to
  // its error handling — for any endpoint that cannot stream, which is most
  // OpenAI-compatible servers people self-host.
  async function chatStream({ config, turns, signal, onReply, onThinking }) {
    const clean = readyConfig(config);
    const request = buildRequest(clean, turns);
    const emit = typeof onReply === 'function' ? onReply : () => {};
    const emitThinking = typeof onThinking === 'function' ? onThinking : () => {};

    let streamed = true;
    let streamFallback = '';
    let usage = null;
    let reply = '';
    let thinking = '';
    const startedAt = Date.now();
    let firstTokenAt = 0;
    const stamp = () => { if (!firstTokenAt) firstTokenAt = Date.now(); };

    if (clean.streaming === false) {
      streamed = false;
      const meta = {};
      ({ reply, thinking } = await sendRequest(clean, request, signal, meta));
      usage = meta.usage || null;
    } else {
      try {
        const result = await sendStreamingRequest(
          clean, request, signal,
          (text) => { stamp(); reply = text; emit(text); },
          (text) => { stamp(); thinking = text; emitThinking(text); }
        );
        reply = result.reply;
        thinking = result.thinking;
        usage = result.usage;
      } catch (error) {
        if (String((error && error.message) || error) !== STREAM_UNSUPPORTED) throw error;
        streamed = false;
        firstTokenAt = 0;
        streamFallback = (error && error.streamReason) || '';
        const meta = {};
        ({ reply, thinking } = await sendRequest(clean, request, signal, meta));
        usage = meta.usage || null;
      }
    }

    return {
      reply: trimTo(reply, MAX_REPLY_CHARS),
      thinking: trimTo(thinking, MAX_THINKING_CHARS),
      streamed,
      streamFallback,
      stats: turnStats({ startedAt, firstTokenAt, streamed, usage })
    };
  }

  return Object.freeze({
    PROVIDERS,
    DEFAULT_PROVIDER,
    MAX_CHAT_TURNS,
    MAX_MESSAGE_CHARS,
    MAX_IMAGES_PER_REQUEST,
    IMAGES_REJECTED_PREFIX,
    STREAM_UNSUPPORTED,
    normalizeConfig,
    loadConfig,
    saveConfig,
    normalizeBaseUrl,
    resolveEndpoint,
    modelsEndpoint,
    listModels,
    sanitizeModels,
    modelLabel,
    extraBodyLooksValid,
    chat,
    chatStream
  });
});
