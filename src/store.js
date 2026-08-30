// Conversation storage for the Obsidian Arc chat.
//
// The API is stateless: every turn re-sends the whole transcript. That is what
// makes "edit an earlier message and run again" a matter of truncating an
// array rather than of reconciling server-side state, so the transcript kept
// here IS the conversation — nothing about it lives anywhere else.
//
// Everything read back out of storage goes through normalizeConversations()
// first: the stored blob is a plain JSON object something else could have
// overwritten (a synced backup, a second tab, a host embedding this
// component), so it is treated as untrusted input on the way in.
//
// `storage` is any object shaped like chrome.storage.local — async
// {get(key), set(obj)} — which is deliberate: an embedding browser extension
// can pass chrome.storage.local directly, with no adapter needed. The
// standalone build passes a small localStorage-backed shim instead (see
// adapters.js).
//
// Loaded as a plain global-scope script (no bundler in this project).

(function (root, factory) {
  const api = factory();
  if (typeof module === 'object' && module.exports) module.exports = api;
  if (root) root.ObsidianStore = api;
})(typeof globalThis !== 'undefined' ? globalThis : this, function () {
  'use strict';

  const STORAGE_KEY = '__obsidian_arc_chats__';

  // Chat history is convenience, not data a user would miss a backup of, so
  // the caps are set low enough that a long-running install cannot quietly
  // grow a multi-megabyte value in local storage.
  const MAX_CONVERSATIONS = 40;
  const MAX_MESSAGES = 120;
  const MAX_CONTENT_CHARS = 8000;
  const MAX_TITLE_CHARS = 60;

  // Attachments. The data URL is the picture itself, so these are the caps
  // that decide how large the stored history can get: image.js keeps one
  // attachment under a megabyte, and a conversation stops carrying the pixels
  // of its older ones once the budget below is used up. Dropping the oldest
  // first matches how the model already sees the conversation, which trims
  // from the same end.
  const MAX_IMAGES_PER_MESSAGE = 4;
  const MAX_IMAGE_CHARS = 2 * 1024 * 1024;
  const MAX_CONVERSATION_IMAGE_CHARS = 12 * 1024 * 1024;
  // Only what both provider shapes accept, and only base64 — a data URL is
  // spliced into an <img> src in the transcript and posted to the API, so a
  // permissive parse here is a hole in both places at once.
  const IMAGE_DATA_URL_RE = /^data:image\/(?:png|jpeg|webp|gif);base64,[A-Za-z0-9+/]+=*$/;
  const MAX_THINKING_CHARS = 20000;

  function isPlainObject(value) {
    return !!value && typeof value === 'object' && !Array.isArray(value);
  }

  function trimTo(value, limit) {
    return typeof value === 'string' ? value.slice(0, limit) : '';
  }

  function newId() {
    const crypto = typeof globalThis !== 'undefined' ? globalThis.crypto : null;
    if (crypto && typeof crypto.randomUUID === 'function') return crypto.randomUUID();
    return `c${Date.now().toString(36)}${Math.random().toString(36).slice(2, 10)}`;
  }

  function normalizeImage(raw) {
    if (!isPlainObject(raw)) return null;
    const dataUrl = typeof raw.dataUrl === 'string' ? raw.dataUrl : '';
    if (dataUrl.length > MAX_IMAGE_CHARS || !IMAGE_DATA_URL_RE.test(dataUrl)) return null;
    const image = { dataUrl, name: trimTo(raw.name, 80) };
    if (Number.isFinite(raw.width) && Number.isFinite(raw.height)) {
      image.width = Math.max(0, Math.round(raw.width));
      image.height = Math.max(0, Math.round(raw.height));
    }
    return image;
  }

  function normalizeImages(raw) {
    return (Array.isArray(raw) ? raw : []).map(normalizeImage).filter(Boolean).slice(0, MAX_IMAGES_PER_MESSAGE);
  }

  // The pictures are kept for the newest messages and dropped from the oldest
  // once the budget is spent. The message itself stays: a bubble that says
  // what was asked reads better than a gap in the transcript.
  function trimImageBudget(messages) {
    let spent = 0;
    for (let index = messages.length - 1; index >= 0; index--) {
      const message = messages[index];
      if (!message.images || !message.images.length) continue;
      const kept = [];
      for (const image of message.images) {
        if (spent + image.dataUrl.length > MAX_CONVERSATION_IMAGE_CHARS) break;
        spent += image.dataUrl.length;
        kept.push(image);
      }
      message.images = kept;
    }
    return messages;
  }

  // What the turn cost and how fast it was. Kept with the message so the
  // numbers survive a reload; every field is optional because plenty of
  // endpoints report no token counts at all.
  function normalizeStats(raw) {
    if (!isPlainObject(raw)) return null;
    const count = (value) => (Number.isFinite(value) && value >= 0 ? value : null);
    const stats = {
      ms: count(raw.ms),
      firstTokenMs: count(raw.firstTokenMs),
      streamed: raw.streamed === true,
      inputTokens: count(raw.inputTokens),
      outputTokens: count(raw.outputTokens),
      tps: count(raw.tps)
    };
    return stats.ms === null ? null : stats;
  }

  function normalizeMessage(raw) {
    if (!isPlainObject(raw)) return null;
    const id = typeof raw.id === 'string' && raw.id ? raw.id.slice(0, 64) : newId();
    const at = Number.isFinite(raw.at) ? raw.at : 0;

    if (raw.role === 'user') {
      const content = trimTo(raw.content, MAX_CONTENT_CHARS);
      return { id, role: 'user', content, images: normalizeImages(raw.images), at };
    }
    if (raw.role !== 'assistant') return null;

    return {
      id,
      role: 'assistant',
      reply: trimTo(raw.reply, MAX_CONTENT_CHARS),
      stats: normalizeStats(raw.stats),
      // The model's own reasoning, shown collapsed under the answer.
      thinking: trimTo(raw.thinking, MAX_THINKING_CHARS),
      error: trimTo(raw.error, 400),
      at
    };
  }

  // The visible title, which is the first thing a user recognises a
  // conversation by: their own opening line. Stored rather than derived so a
  // rename sticks.
  function deriveTitle(conversation) {
    const first = conversation.messages.find((message) => message.role === 'user' && message.content.trim());
    return first ? first.content.trim().replace(/\s+/g, ' ').slice(0, MAX_TITLE_CHARS) : '';
  }

  function normalizeConversation(raw) {
    if (!isPlainObject(raw)) return null;
    const messages = trimImageBudget((Array.isArray(raw.messages) ? raw.messages : [])
      .map(normalizeMessage)
      .filter(Boolean)
      .slice(-MAX_MESSAGES));

    const conversation = {
      id: typeof raw.id === 'string' && raw.id ? raw.id.slice(0, 64) : newId(),
      title: trimTo(raw.title, MAX_TITLE_CHARS),
      messages,
      createdAt: Number.isFinite(raw.createdAt) ? raw.createdAt : 0,
      updatedAt: Number.isFinite(raw.updatedAt) ? raw.updatedAt : 0
    };
    if (!conversation.title) conversation.title = deriveTitle(conversation);
    return conversation;
  }

  function normalizeConversations(raw) {
    const list = Array.isArray(raw) ? raw : (isPlainObject(raw) && Array.isArray(raw.conversations) ? raw.conversations : []);
    return list
      .map(normalizeConversation)
      .filter(Boolean)
      .sort((a, b) => (b.updatedAt || 0) - (a.updatedAt || 0))
      .slice(0, MAX_CONVERSATIONS);
  }

  function createConversation({ at = 0 } = {}) {
    return { id: newId(), title: '', messages: [], createdAt: at, updatedAt: at };
  }

  function userMessage(content, at = 0, images = []) {
    return { id: newId(), role: 'user', content: trimTo(content, MAX_CONTENT_CHARS), images: normalizeImages(images), at };
  }

  function assistantMessage(answer, at = 0) {
    const source = isPlainObject(answer) ? answer : {};
    return {
      id: newId(),
      role: 'assistant',
      reply: trimTo(source.reply, MAX_CONTENT_CHARS),
      stats: normalizeStats(source.stats),
      thinking: trimTo(source.thinking, MAX_THINKING_CHARS),
      error: trimTo(source.error, 400),
      at
    };
  }

  // What the model is shown. Failed turns are left out entirely: replaying "I
  // could not reach the API" as though the assistant had said it teaches the
  // model that refusing is a valid answer shape.
  function toTurns(conversation) {
    return (conversation && Array.isArray(conversation.messages) ? conversation.messages : [])
      .filter((message) => message.role === 'user' || (!message.error && message.reply))
      .map((message) => {
        if (message.role !== 'user') return { role: 'assistant', content: message.reply };
        // Omitted rather than sent empty: a turn with no attachment is the
        // common case, and an empty array would travel on every one of them.
        const turn = { role: 'user', content: message.content };
        if (message.images && message.images.length) turn.images = message.images;
        return turn;
      });
  }

  // Editing a message rewrites history from that point: everything after it
  // was an answer to a question that no longer exists.
  function truncateFrom(conversation, messageId) {
    const index = conversation.messages.findIndex((message) => message.id === messageId);
    if (index === -1) return conversation.messages.slice();
    return conversation.messages.slice(0, index);
  }

  async function load(storage) {
    const data = await storage.get(STORAGE_KEY);
    return normalizeConversations(data && data[STORAGE_KEY]);
  }

  async function save(storage, conversations) {
    const list = normalizeConversations(conversations);
    await storage.set({ [STORAGE_KEY]: list });
    return list;
  }

  return Object.freeze({
    STORAGE_KEY,
    MAX_CONVERSATIONS,
    MAX_MESSAGES,
    MAX_CONTENT_CHARS,
    MAX_IMAGES_PER_MESSAGE,
    MAX_IMAGE_CHARS,
    MAX_CONVERSATION_IMAGE_CHARS,
    newId,
    deriveTitle,
    normalizeMessage,
    normalizeImages,
    normalizeConversation,
    normalizeConversations,
    createConversation,
    userMessage,
    assistantMessage,
    toTurns,
    truncateFrom,
    load,
    save
  });
});
