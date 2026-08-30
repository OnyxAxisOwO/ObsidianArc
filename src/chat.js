// The chat surface: history, composing, streaming, editing, markdown,
// attachments. Everything a host page needs to decide for itself arrives as a
// mount() option — how a turn is actually answered, where conversations are
// stored, what "configured" means — so this file has no idea whether it is
// running standalone or embedded in something else.
//
// Every turn re-sends the whole transcript (see src/store.js), which is what
// makes editing an earlier message work: truncate the array at that message,
// append the rewritten one, and ask again. There is no server state to
// reconcile.
//
// Model output is never given to innerHTML. Prose goes through
// src/markdown.js, which builds elements. Attachments get the same
// treatment: a data URL is checked against the same pattern the store uses
// before it becomes an <img> src.

(function (root, factory) {
  const api = factory();
  if (typeof module === 'object' && module.exports) module.exports = api;
  if (root) root.ObsidianChat = api;
})(typeof globalThis !== 'undefined' ? globalThis : this, function () {
  'use strict';

  const IMAGE_DATA_URL_RE = /^data:image\/(?:png|jpeg|webp|gif);base64,[A-Za-z0-9+/]+=*$/;
  const SVG_NS = 'http://www.w3.org/2000/svg';
  const MAX_MESSAGE_CHARS = 4000;

  const PAPERCLIP = [
    'M21.44 11.05l-9.19 9.19a6 6 0 0 1-8.49-8.49l9.19-9.19a4 4 0 0 1 5.66 5.66l-9.2 9.19a2 2 0 0 1-2.83-2.83l8.49-8.48'
  ];
  const CROSS = ['M18 6L6 18', 'M6 6l12 12'];
  const MENU = ['M3 6h18', 'M3 12h18', 'M3 18h18'];

  // The opening prompts. Far more than fit on screen, so each visit offers a
  // different three — a fixed trio is read once and then becomes furniture,
  // while a rotating one keeps suggesting things the user did not know to ask
  // for.
  const SUGGESTION_KEYS = [
    'suggestionExplain', 'suggestionBrainstorm', 'suggestionWrite', 'suggestionSummarize',
    'suggestionDebug', 'suggestionPlan', 'suggestionCompare', 'suggestionRewrite',
    'suggestionLearn', 'suggestionDecide', 'suggestionDraft', 'suggestionCritique'
  ];
  const SUGGESTIONS_SHOWN = 3;

  function pickSuggestions(count) {
    const pool = SUGGESTION_KEYS.slice();
    const picked = [];
    while (picked.length < count && pool.length) {
      picked.push(pool.splice(Math.floor(Math.random() * pool.length), 1)[0]);
    }
    return picked;
  }

  const STRINGS = {
    en: {
      chatTitle: 'Obsidian Arc',
      newChat: 'New chat',
      history: 'History',
      noHistory: 'No conversations yet.',
      deleteChat: 'Delete this conversation',
      confirmDelete: 'Delete this conversation?',
      clearAll: 'Clear all',
      confirmClearAll: 'Delete all conversations? This cannot be undone.',
      placeholder: 'Send a message…',
      placeholderFirst: 'What do you want to talk about?',
      send: 'Send',
      attach: 'Attach an image',
      removeImage: 'Remove this image',
      dropHint: 'Drop images here',
      tooManyImages: 'Up to {count} images per message.',
      imageTooLarge: 'That image is too large.',
      imageFailed: 'That image could not be read.',
      imagesUnsupported: 'This model cannot read images. Pick a vision-capable model, or edit the message and remove the picture.',
      streamFallback: 'Streaming was not available this time, so the whole answer arrived at once — {reason}.',
      statsStreamed: 'streamed',
      statsOneShot: 'one-shot',
      statsFirstToken: 'first token {seconds}s',
      statsTokens: '{input} in / {output} out',
      statsOutputOnly: '{output} out',
      statsSpeed: '{tps} tok/s',
      reasoning: 'Reasoning',
      reasoningLive: 'Thinking…',
      stop: 'Stop',
      stopped: 'Stopped.',
      thinking: 'Thinking…',
      emptyTitle: 'Start a conversation',
      emptyBody: 'Bring your own API key — Anthropic, or any OpenAI-compatible endpoint — and talk to it here. Nothing leaves your browser except what you send.',
      suggestionExplain: 'Explain a concept simply',
      suggestionBrainstorm: 'Brainstorm names for a project',
      suggestionWrite: 'Write a first draft of something',
      suggestionSummarize: 'Summarize a long piece of text',
      suggestionDebug: 'Help me debug an error',
      suggestionPlan: 'Plan out a weekend trip',
      suggestionCompare: 'Compare two options for me',
      suggestionRewrite: 'Rewrite this in a different tone',
      suggestionLearn: 'Teach me something new',
      suggestionDecide: 'Help me think through a decision',
      suggestionDraft: 'Draft an email for me',
      suggestionCritique: 'Give me honest feedback on an idea',
      setupTitle: 'Add an API key to start',
      setupBody: 'The chat runs on your own API key — Anthropic, or any OpenAI-compatible endpoint. Nothing is sent anywhere until you add one.',
      setupAction: 'Open settings',
      edit: 'Edit',
      copy: 'Copy',
      copied: 'Copied',
      cancel: 'Cancel',
      saveAndResend: 'Save & resend',
      regenerate: 'Regenerate',
      retry: 'Try again',
      failed: 'Something went wrong.'
    },
    zh: {
      chatTitle: 'Obsidian Arc',
      newChat: '新对话',
      history: '历史记录',
      noHistory: '还没有对话记录。',
      deleteChat: '删除这个对话',
      confirmDelete: '删除这个对话？',
      clearAll: '清空全部',
      confirmClearAll: '删除全部对话？此操作无法撤销。',
      placeholder: '发送消息…',
      placeholderFirst: '想聊点什么？',
      send: '发送',
      attach: '添加图片',
      removeImage: '移除这张图片',
      dropHint: '把图片拖到这里',
      tooManyImages: '每条消息最多 {count} 张图片。',
      imageTooLarge: '这张图片太大了。',
      imageFailed: '这张图片读不出来。',
      imagesUnsupported: '这个模型看不了图片。请换一个支持看图的模型，或者编辑这条消息把图片去掉。',
      streamFallback: '这次没能流式返回，整段答案一次性到达——{reason}。',
      statsStreamed: '流式',
      statsOneShot: '一次性',
      statsFirstToken: '首字 {seconds}s',
      statsTokens: '输入 {input} / 输出 {output}',
      statsOutputOnly: '输出 {output}',
      statsSpeed: '{tps} tok/s',
      reasoning: '思考过程',
      reasoningLive: '正在思考…',
      stop: '停止',
      stopped: '已停止。',
      thinking: '正在思考…',
      emptyTitle: '开始一段对话',
      emptyBody: '使用你自己的 API 密钥——支持 Anthropic 和任意 OpenAI 兼容接口。除了你发送的内容，不会有任何数据离开你的浏览器。',
      suggestionExplain: '用简单的话解释一个概念',
      suggestionBrainstorm: '帮项目想几个名字',
      suggestionWrite: '写一段初稿',
      suggestionSummarize: '总结一大段文字',
      suggestionDebug: '帮我调试一个报错',
      suggestionPlan: '规划一次周末出行',
      suggestionCompare: '帮我比较两个选项',
      suggestionRewrite: '换一种语气重写',
      suggestionLearn: '教我一些新东西',
      suggestionDecide: '帮我理清一个决定',
      suggestionDraft: '帮我起草一封邮件',
      suggestionCritique: '给我一些坦率的反馈',
      setupTitle: '先填一个 API 密钥',
      setupBody: '对话使用你自己的 API 密钥，支持 Anthropic 和任意 OpenAI 兼容接口。在你填写之前，不会向任何地方发送数据。',
      setupAction: '打开设置',
      edit: '编辑',
      copy: '复制',
      copied: '已复制',
      cancel: '取消',
      saveAndResend: '保存并重新发送',
      regenerate: '重新生成',
      retry: '重试',
      failed: '出错了。'
    }
  };

  function makeDom(doc) {
    function el(tag, className, text) {
      const node = doc.createElement(tag);
      if (className) node.className = className;
      if (text != null) node.textContent = text;
      return node;
    }
    function button(className, text, onClick) {
      const node = el('button', className, text);
      node.type = 'button';
      if (onClick) node.addEventListener('click', onClick);
      return node;
    }
    function icon(paths, size) {
      const svg = doc.createElementNS(SVG_NS, 'svg');
      svg.setAttribute('viewBox', '0 0 24 24');
      svg.setAttribute('width', String(size || 16));
      svg.setAttribute('height', String(size || 16));
      svg.setAttribute('fill', 'none');
      svg.setAttribute('stroke', 'currentColor');
      svg.setAttribute('stroke-width', '2');
      svg.setAttribute('stroke-linecap', 'round');
      svg.setAttribute('stroke-linejoin', 'round');
      svg.setAttribute('aria-hidden', 'true');
      paths.forEach((d) => {
        const path = doc.createElementNS(SVG_NS, 'path');
        path.setAttribute('d', d);
        svg.appendChild(path);
      });
      return svg;
    }
    return { el, button, icon };
  }

  function safeImageSrc(dataUrl) {
    const candidate = typeof dataUrl === 'string' ? dataUrl.trim() : '';
    return IMAGE_DATA_URL_RE.test(candidate) ? candidate : '';
  }

  function mount(config) {
    const host = config.root;
    if (!host) throw new Error('ObsidianChat.mount needs a root element.');

    const doc = host.ownerDocument;
    const { el, button, icon } = makeDom(doc);
    const storage = config.storage;
    if (!storage) throw new Error('ObsidianChat.mount needs a storage adapter.');
    const Store = config.store || (typeof globalThis !== 'undefined' ? globalThis.ObsidianStore : null);
    const Markdown = config.markdown || (typeof globalThis !== 'undefined' ? globalThis.ObsidianMarkdown : null);
    const imageApi = () => config.image || (doc.defaultView && doc.defaultView.ObsidianImage) || null;
    const maxImages = (Store && Store.MAX_IMAGES_PER_MESSAGE) || 4;
    const variant = config.variant === 'wide' ? 'wide' : 'narrow';
    const lang = STRINGS[config.lang] ? config.lang : 'en';
    // Answers one turn. Resolves to {reply, thinking, stats, streamed,
    // streamFallback}; onReply/onThinking fire as text streams in. Throwing
    // (including an AbortError from `signal`) is how a failure is reported —
    // there is no separate {ok:false} shape to check.
    const send = config.send;
    if (typeof send !== 'function') throw new Error('ObsidianChat.mount needs a send(...) function.');
    // Cheap status check the host can answer however it likes — whether an
    // API key is configured, whether the active model reads images, whether
    // streaming is turned on. Re-run on demand via the returned
    // refreshStatus(), typically after the settings panel saves a change.
    const getStatus = config.getStatus || (async () => ({ configured: true, vision: false, streaming: true }));
    const openSettings = config.openSettings || (() => {});
    // A host-owned row (model picker, settings toggle…) parked above the
    // transcript. The component only places it; what it does is the host's.
    const mainHeader = config.mainHeader || null;

    function str(key, vars) {
      const value = (STRINGS[lang] && STRINGS[lang][key]) || STRINGS.en[key] || key;
      return vars ? value.replace(/\{(\w+)\}/g, (match, name) => (name in vars ? String(vars[name]) : match)) : value;
    }

    const shownSuggestions = pickSuggestions(SUGGESTIONS_SHOWN);

    let conversations = [];
    let activeId = '';
    let busy = false;
    let cancelTurn = null;
    let editingId = '';
    let justSentId = '';
    let configured = false;
    let visionEnabled = false;
    let streamingEnabled = true;
    let historyOpen = false;
    let lastWritten = '';
    let flash = '';
    let pendingImages = [];
    let dragging = false;
    let streamingReply = '';
    let streamingThinking = '';
    let pendingNodes = null;
    let lastRenderedId = null;

    // --- structure ------------------------------------------------------------

    host.classList.add('ai-chat', `ai-chat-${variant}`);
    host.textContent = '';

    const sidebar = el('div', 'ai-chat-sidebar');
    const sidebarHead = el('div', 'ai-chat-sidebar-head');
    const newChatBtn = button('ai-chat-new', '', () => startNewConversation());
    newChatBtn.appendChild(icon(['M12 5v14', 'M5 12h14'], 14));
    newChatBtn.appendChild(el('span', null, str('newChat')));
    sidebarHead.appendChild(el('span', 'ai-chat-sidebar-title', str('history')));
    sidebarHead.appendChild(newChatBtn);
    const clearAllBtn = button('ai-chat-clear-all', '', async () => {
      if (!conversations.length) return;
      if (!doc.defaultView.confirm(str('confirmClearAll'))) return;
      conversations = [];
      activeId = '';
      await persist();
      await startNewConversation();
    });
    clearAllBtn.appendChild(icon(['M3 6h18', 'M8 6V4h8v2', 'M19 6l-1 14H6L5 6', 'M10 11v6', 'M14 11v6'], 12));
    clearAllBtn.appendChild(el('span', null, str('clearAll')));
    const sidebarFoot = el('div', 'ai-chat-sidebar-foot');
    sidebarFoot.appendChild(clearAllBtn);
    const sidebarList = el('div', 'ai-chat-list');
    sidebar.appendChild(sidebarHead);
    sidebar.appendChild(sidebarList);
    sidebar.appendChild(sidebarFoot);

    const main = el('div', 'ai-chat-main');
    const bar = el('div', 'ai-chat-bar');
    const historyBtn = button('ai-chat-bar-btn', '', () => {
      historyOpen = !historyOpen;
      render();
    });
    historyBtn.appendChild(icon(MENU, 16));
    historyBtn.title = str('history');
    historyBtn.setAttribute('aria-label', str('history'));
    const barTitle = el('span', 'ai-chat-bar-title', str('chatTitle'));
    const barNewBtn = button('ai-chat-bar-btn', '', () => startNewConversation());
    barNewBtn.appendChild(icon(['M12 5v14', 'M5 12h14'], 15));
    barNewBtn.title = str('newChat');
    barNewBtn.setAttribute('aria-label', str('newChat'));
    bar.appendChild(historyBtn);
    bar.appendChild(barTitle);
    bar.appendChild(barNewBtn);

    const scroll = el('div', 'ai-chat-scroll');
    const flashLine = el('div', 'ai-chat-flash');
    flashLine.setAttribute('role', 'status');
    flashLine.setAttribute('aria-live', 'polite');

    const composer = el('form', 'ai-chat-composer');
    const pendingStrip = el('div', 'ai-chat-attachments');
    const composerRow = el('div', 'ai-chat-composer-row');
    const input = el('textarea', 'ai-chat-input');
    input.rows = 1;
    input.spellcheck = false;
    input.maxLength = MAX_MESSAGE_CHARS;
    const fileInput = el('input', 'ai-chat-file');
    fileInput.type = 'file';
    fileInput.accept = 'image/*';
    fileInput.multiple = true;
    fileInput.hidden = true;
    const attachBtn = button('ai-chat-attach', '', () => fileInput.click());
    attachBtn.appendChild(icon(PAPERCLIP, 16));
    attachBtn.title = str('attach');
    attachBtn.setAttribute('aria-label', str('attach'));
    const sendBtn = button('ai-chat-send', '');
    const sendIcon = icon(['M12 19V5', 'M5 12l7-7 7 7'], 16);
    const stopIcon = icon(['M7 7h10v10H7z'], 14);
    stopIcon.style.display = 'none';
    sendBtn.appendChild(sendIcon);
    sendBtn.appendChild(stopIcon);
    sendBtn.title = str('send');
    sendBtn.setAttribute('aria-label', str('send'));
    composerRow.appendChild(attachBtn);
    composerRow.appendChild(input);
    composerRow.appendChild(sendBtn);
    composer.appendChild(pendingStrip);
    composer.appendChild(composerRow);
    composer.appendChild(fileInput);

    const dropHint = el('div', 'ai-chat-drop', str('dropHint'));

    main.appendChild(bar);
    if (mainHeader) main.appendChild(mainHeader);
    main.appendChild(scroll);
    main.appendChild(flashLine);
    main.appendChild(composer);
    main.appendChild(dropHint);
    host.appendChild(sidebar);
    host.appendChild(main);

    // --- state helpers --------------------------------------------------------

    function active() {
      return byId(activeId);
    }

    function byId(id) {
      return conversations.find((entry) => entry.id === id) || null;
    }

    async function persist() {
      const saved = await Store.save(storage, conversations);
      conversations = saved;
      lastWritten = JSON.stringify(saved);
      if (!active() && saved.length) activeId = saved[0].id;
    }

    function setFlash(text) {
      flash = text || '';
      flashLine.textContent = flash;
      flashLine.classList.toggle('visible', !!flash);
    }

    function scrollToEnd() {
      scroll.scrollTop = scroll.scrollHeight;
    }

    async function copyText(text) {
      try {
        await doc.defaultView.navigator.clipboard.writeText(text);
        setFlash(str('copied'));
      } catch (_) {
        // Clipboard permission is not guaranteed in every browser, and a
        // failed copy is not worth an error state in the transcript.
      }
    }

    // --- attachments ------------------------------------------------------

    function thumbnail(image, onRemove) {
      const chip = el('div', 'ai-chat-attachment');
      const src = safeImageSrc(image && image.dataUrl);
      if (!src) return null;
      const thumb = el('img', 'ai-chat-attachment-img');
      thumb.src = src;
      thumb.alt = (image && image.name) || '';
      thumb.draggable = false;
      chip.appendChild(thumb);
      if (image && image.name) chip.title = image.name;
      if (onRemove) {
        const remove = button('ai-chat-attachment-remove', '', onRemove);
        remove.appendChild(icon(CROSS, 11));
        remove.title = str('removeImage');
        remove.setAttribute('aria-label', str('removeImage'));
        chip.appendChild(remove);
      }
      return chip;
    }

    function renderAttachmentStrip(images, editable) {
      const strip = el('div', 'ai-chat-attachments');
      const paint = () => {
        strip.textContent = '';
        images.forEach((image, index) => {
          const chip = thumbnail(image, editable ? () => {
            images.splice(index, 1);
            paint();
          } : null);
          if (chip) strip.appendChild(chip);
        });
      };
      paint();
      return strip;
    }

    function renderComposerAttachments() {
      pendingStrip.textContent = '';
      pendingImages.forEach((image, index) => {
        const chip = thumbnail(image, () => {
          pendingImages.splice(index, 1);
          renderComposerAttachments();
        });
        if (chip) pendingStrip.appendChild(chip);
      });
      attachBtn.hidden = !visionEnabled;
      attachBtn.disabled = busy || !configured || pendingImages.length >= maxImages;
    }

    // One at a time rather than in parallel: each file is decoded and
    // re-encoded through a canvas, and a handful of large photos at once is
    // enough to lock up the page for a noticeable moment.
    async function addFiles(fileList) {
      if (busy || !configured || !visionEnabled) return;
      const files = Array.from(fileList || []).filter((file) => file && file.type && file.type.startsWith('image/'));
      if (!files.length) return;

      const room = maxImages - pendingImages.length;
      if (files.length > room) setFlash(str('tooManyImages', { count: maxImages }));
      if (room <= 0) return;

      const api = imageApi();
      if (!api || typeof api.prepareChatImage !== 'function') {
        setFlash(str('imageFailed'));
        return;
      }
      for (const file of files.slice(0, room)) {
        try {
          const prepared = await api.prepareChatImage(file);
          pendingImages.push({
            dataUrl: prepared.dataUrl,
            name: prepared.name,
            width: prepared.width,
            height: prepared.height
          });
          renderComposerAttachments();
        } catch (error) {
          setFlash(/too large/i.test(String((error && error.message) || error)) ? str('imageTooLarge') : str('imageFailed'));
        }
      }
    }

    function carriesFiles(event) {
      if (!visionEnabled) return false;
      const types = event.dataTransfer && event.dataTransfer.types;
      return !!types && Array.prototype.indexOf.call(types, 'Files') !== -1;
    }

    function setDragging(state) {
      if (dragging === state) return;
      dragging = state;
      host.classList.toggle('dragging', state);
    }

    // --- rendering --------------------------------------------------------

    function renderSidebar(switched) {
      sidebarFoot.hidden = !conversations.length;
      sidebarList.textContent = '';
      if (!conversations.length) {
        sidebarList.appendChild(el('p', 'ai-chat-list-empty', str('noHistory')));
        return;
      }
      conversations.forEach((conversation) => {
        const isActive = conversation.id === activeId;
        const row = el('div', `ai-chat-list-item${isActive ? ' active' : ''}${isActive && switched ? ' switched' : ''}`);
        const open = button('ai-chat-list-open', '');
        open.appendChild(el('span', 'ai-chat-list-title', conversation.title || str('newChat')));
        open.addEventListener('click', () => {
          activeId = conversation.id;
          editingId = '';
          pendingImages = [];
          historyOpen = false;
          render();
          scrollToEnd();
        });
        const remove = button('ai-chat-list-delete', '', async () => {
          if (!doc.defaultView.confirm(str('confirmDelete'))) return;
          conversations = conversations.filter((entry) => entry.id !== conversation.id);
          if (activeId === conversation.id) activeId = conversations.length ? conversations[0].id : '';
          await persist();
          if (!activeId) await startNewConversation();
          else render();
        });
        remove.appendChild(icon(['M3 6h18', 'M8 6V4h8v2', 'M19 6l-1 14H6L5 6', 'M10 11v6', 'M14 11v6'], 13));
        remove.title = str('deleteChat');
        remove.setAttribute('aria-label', str('deleteChat'));
        row.appendChild(open);
        row.appendChild(remove);
        sidebarList.appendChild(row);
      });
    }

    function renderSetup() {
      const card = el('div', 'ai-chat-setup');
      card.appendChild(el('h3', 'ai-chat-setup-title', str('setupTitle')));
      card.appendChild(el('p', 'ai-chat-setup-body', str('setupBody')));
      card.appendChild(button('ai-chat-setup-action', str('setupAction'), () => openSettings()));
      return card;
    }

    function renderEmpty() {
      const empty = el('div', 'ai-chat-empty');
      empty.appendChild(el('h3', 'ai-chat-empty-title', str('emptyTitle')));
      empty.appendChild(el('p', 'ai-chat-empty-body', str('emptyBody')));
      const suggestions = el('div', 'ai-chat-suggestions');
      shownSuggestions.forEach((key) => {
        suggestions.appendChild(button('ai-chat-suggestion', str(key), () => {
          input.value = str(key);
          submit();
        }));
      });
      empty.appendChild(suggestions);
      return empty;
    }

    function renderUserMessage(conversation, message) {
      const row = el('div', 'ai-msg ai-msg-user');
      if (message.id === justSentId) row.classList.add('ai-msg-sent');
      const images = Array.isArray(message.images) ? message.images : [];
      const edited = editingId === message.id ? images.slice() : images;
      if (edited.length) row.appendChild(renderAttachmentStrip(edited, editingId === message.id));
      if (editingId === message.id) {
        const editor = el('div', 'ai-msg-editor');
        const area = el('textarea', 'ai-chat-input ai-msg-edit-input');
        area.maxLength = MAX_MESSAGE_CHARS;
        area.value = message.content;
        area.rows = Math.min(8, Math.max(2, message.content.split('\n').length + 1));
        const actions = el('div', 'ai-msg-editor-actions');
        actions.appendChild(button('ai-chat-mini-btn', str('cancel'), () => {
          editingId = '';
          render();
        }));
        actions.appendChild(button('ai-chat-mini-btn primary', str('saveAndResend'), async () => {
          const text = area.value.trim();
          editingId = '';
          conversation.messages = Store.truncateFrom(conversation, message.id);
          const resent = Store.userMessage(text, Date.now(), edited);
          conversation.messages.push(resent);
          conversation.updatedAt = Date.now();
          justSentId = resent.id;
          await persist();
          render();
          await runTurn(conversation.id);
        }));
        editor.appendChild(area);
        editor.appendChild(actions);
        row.appendChild(editor);
        return row;
      }

      if (message.content || !edited.length) row.appendChild(el('div', 'ai-bubble', message.content));
      const actions = el('div', 'ai-msg-actions');
      actions.appendChild(button('ai-chat-mini-btn', str('edit'), () => {
        editingId = message.id;
        render();
      }));
      actions.appendChild(button('ai-chat-mini-btn', str('copy'), () => copyText(message.content)));
      row.appendChild(actions);
      return row;
    }

    function renderThinking(text, live) {
      const box = el('details', 'ai-thinking');
      if (live) box.open = true;
      const head = el('summary', 'ai-thinking-head', str(live ? 'reasoningLive' : 'reasoning'));
      box.appendChild(head);
      const body = el('div', 'ai-thinking-body', text);
      box.appendChild(body);
      if (live) requestAnimationFrame(() => { body.scrollTop = body.scrollHeight; });
      return box;
    }

    function renderStats(stats) {
      const seconds = (ms) => (ms >= 10000 ? String(Math.round(ms / 1000)) : (Math.round(ms / 100) / 10).toFixed(1));
      const parts = [`${seconds(stats.ms)}s`];
      parts.push(str(stats.streamed ? 'statsStreamed' : 'statsOneShot'));
      if (stats.firstTokenMs !== null && stats.firstTokenMs !== undefined) {
        parts.push(str('statsFirstToken', { seconds: seconds(stats.firstTokenMs) }));
      }
      if (stats.outputTokens !== null && stats.outputTokens !== undefined) {
        parts.push(stats.inputTokens === null || stats.inputTokens === undefined
          ? str('statsOutputOnly', { output: stats.outputTokens })
          : str('statsTokens', { input: stats.inputTokens, output: stats.outputTokens }));
      }
      if (stats.tps !== null && stats.tps !== undefined) parts.push(str('statsSpeed', { tps: stats.tps }));
      return el('div', 'ai-msg-stats', parts.join(' · '));
    }

    function renderAssistantMessage(conversation, message) {
      const row = el('div', 'ai-msg ai-msg-assistant');

      if (message.error) {
        const failure = el('div', 'ai-chat-error');
        failure.appendChild(el('span', 'ai-chat-error-text', message.error));
        failure.appendChild(button('ai-chat-mini-btn', str('retry'), async () => {
          conversation.messages = Store.truncateFrom(conversation, message.id);
          await persist();
          render();
          await runTurn(conversation.id);
        }));
        row.appendChild(failure);
        return row;
      }

      if (message.thinking) row.appendChild(renderThinking(message.thinking, false));

      const answer = el('div', 'ai-answer');
      Markdown.renderInto(answer, message.reply);
      row.appendChild(answer);

      const actions = el('div', 'ai-msg-actions');
      actions.appendChild(button('ai-chat-mini-btn', str('regenerate'), async () => {
        conversation.messages = Store.truncateFrom(conversation, message.id);
        await persist();
        render();
        await runTurn(conversation.id);
      }));
      actions.appendChild(button('ai-chat-mini-btn', str('copy'), () => copyText(message.reply)));
      row.appendChild(actions);
      if (message.stats) row.appendChild(renderStats(message.stats));
      return row;
    }

    function renderPending(conversation) {
      const row = el('div', 'ai-msg ai-msg-assistant');
      pendingNodes = { thinking: null, answer: null };
      if (streamingThinking) {
        const box = renderThinking(streamingThinking, true);
        pendingNodes.thinking = box.querySelector('.ai-thinking-body');
        row.appendChild(box);
      }
      if (streamingReply) {
        const answer = el('div', 'ai-answer ai-answer-streaming');
        Markdown.renderInto(answer, streamingReply);
        pendingNodes.answer = answer;
        row.appendChild(answer);
        return row;
      }
      const pending = el('div', 'ai-chat-pending');
      pending.appendChild(el('span', 'ai-chat-spinner'));
      pending.appendChild(el('span', null, str('thinking')));
      row.appendChild(pending);
      return row;
    }

    function updatePending() {
      if (!pendingNodes) return false;
      if (!!streamingThinking !== !!pendingNodes.thinking) return false;
      if (!!streamingReply !== !!pendingNodes.answer) return false;
      const box = pendingNodes.thinking;
      if (box) {
        const atEnd = box.scrollHeight - box.scrollTop - box.clientHeight < 24;
        box.textContent = streamingThinking;
        if (atEnd) box.scrollTop = box.scrollHeight;
      }
      if (pendingNodes.answer) Markdown.renderInto(pendingNodes.answer, streamingReply);
      return true;
    }

    function render() {
      pendingNodes = null;
      const conversation = active();
      const switched = activeId !== lastRenderedId;
      lastRenderedId = activeId;
      host.classList.toggle('history-open', historyOpen);
      barTitle.textContent = (conversation && conversation.title) || str('chatTitle');
      renderSidebar(switched);

      if (switched) {
        scroll.classList.remove('ai-chat-switching');
        void scroll.offsetWidth;
        scroll.classList.add('ai-chat-switching');
      }

      scroll.textContent = '';
      if (!configured) scroll.appendChild(renderSetup());
      if (conversation && conversation.messages.length) {
        conversation.messages.forEach((message) => {
          scroll.appendChild(message.role === 'user'
            ? renderUserMessage(conversation, message)
            : renderAssistantMessage(conversation, message));
        });
      } else if (configured) {
        scroll.appendChild(renderEmpty());
      }
      if (busy && conversation) scroll.appendChild(renderPending(conversation));

      const empty = !conversation || !conversation.messages.length;
      host.classList.toggle('is-empty', empty);
      input.placeholder = str(empty ? 'placeholderFirst' : 'placeholder');
      input.disabled = busy || !configured;
      syncSendButton();
      renderComposerAttachments();
    }

    function syncSendButton() {
      const stoppable = busy && !!cancelTurn;
      sendBtn.classList.toggle('stop', stoppable);
      sendBtn.disabled = busy ? !stoppable : !configured;
      sendIcon.style.display = stoppable ? 'none' : '';
      stopIcon.style.display = stoppable ? '' : 'none';
      const label = str(stoppable ? 'stop' : 'send');
      sendBtn.title = label;
      sendBtn.setAttribute('aria-label', label);
    }

    // --- turns --------------------------------------------------------------

    function friendlyError(message) {
      if (/No API key|No model/i.test(message)) {
        configured = false;
        return str('setupBody');
      }
      const rejectedImage = message.match(/^This model or endpoint did not accept an attached image\.\s*(.*)$/is);
      if (rejectedImage) return `${str('imagesUnsupported')} ${rejectedImage[1]}`.trim();
      return message || str('failed');
    }

    // One turn. Cancellation is a plain AbortController: aborting it is what
    // "Stop" does, and a `send` built on fetch (see src/adapters.js) treats an
    // aborted signal as the reason its own promise rejects. That rejection is
    // caught below and treated as a deliberate stop rather than a failure —
    // whatever had already streamed into streamingReply/streamingThinking by
    // then is kept, because it is what the user read while deciding to stop.
    function requestTurn(turns, onDelta, onThinking) {
      const controller = new AbortController();
      cancelTurn = () => controller.abort();
      syncSendButton();
      return send({ turns, signal: controller.signal, onReply: onDelta, onThinking })
        .then((result) => ({ ok: true, ...result }))
        .catch((error) => {
          if (controller.signal.aborted) return { ok: true, stopped: true };
          return { ok: false, error: String((error && error.message) || error) };
        });
    }

    async function runTurn(conversationId) {
      if (busy) return;
      const conversation = byId(conversationId);
      if (!conversation) return;
      busy = true;
      streamingReply = '';
      streamingThinking = '';
      setFlash('');
      render();
      justSentId = '';
      scrollToEnd();

      try {
        const stream = (apply) => {
          if (!busy || activeId !== conversationId) return;
          const atEnd = scroll.scrollHeight - scroll.scrollTop - scroll.clientHeight < 40;
          apply();
          if (!updatePending()) render();
          if (atEnd) scrollToEnd();
        };
        const response = await requestTurn(
          Store.toTurns(conversation),
          (text) => stream(() => { streamingReply = text; }),
          (text) => stream(() => { streamingThinking = text; })
        );
        if (!response) throw new Error(str('failed'));
        if (!response.ok) throw new Error(response.error || str('failed'));
        if (response.stopped) {
          const at = Date.now();
          const target = byId(conversationId);
          if (!target) return;
          const partial = streamingReply.trim();
          if (partial) {
            target.messages.push(Store.assistantMessage({ reply: partial, thinking: streamingThinking }, at));
            target.updatedAt = at;
            await persist();
          }
          streamingReply = '';
          streamingThinking = '';
          busy = false;
          setFlash(str('stopped'));
          render();
          scrollToEnd();
          return;
        }
        const streamNotice = streamingEnabled && response.streamed === false && response.streamFallback
          ? str('streamFallback', { reason: response.streamFallback })
          : '';

        const at = Date.now();
        const target = byId(conversationId);
        if (!target) return;
        target.messages.push(Store.assistantMessage(response, at));
        target.updatedAt = at;
        if (!target.title) target.title = Store.deriveTitle(target);
        await persist();
        streamingReply = '';
        streamingThinking = '';
        busy = false;
        render();
        scrollToEnd();
        if (streamNotice) setFlash(streamNotice);
      } catch (error) {
        const at = Date.now();
        const target = byId(conversationId);
        if (!target) return;
        target.messages.push(Store.assistantMessage({ error: friendlyError(String((error && error.message) || error)) }, at));
        target.updatedAt = at;
        await persist();
        streamingReply = '';
        streamingThinking = '';
        busy = false;
        render();
        scrollToEnd();
      } finally {
        streamingReply = '';
        streamingThinking = '';
        busy = false;
        cancelTurn = null;
      }
    }

    async function submit() {
      if (busy || !configured) return;
      let conversation = active();
      if (!conversation) conversation = await startNewConversation({ silent: true });

      const text = input.value.trim();
      const images = pendingImages.slice();
      if (!text && !images.length && conversation.messages.length) return;

      input.value = '';
      pendingImages = [];
      renderComposerAttachments();
      resizeInput();
      const at = Date.now();
      const sent = Store.userMessage(text, at, images);
      conversation.messages.push(sent);
      conversation.updatedAt = at;
      if (!conversation.title) conversation.title = Store.deriveTitle(conversation);
      justSentId = sent.id;
      await persist();
      render();
      scrollToEnd();
      await runTurn(conversation.id);
    }

    async function startNewConversation({ silent = false } = {}) {
      const conversation = Store.createConversation({ at: Date.now() });
      conversations = [conversation, ...conversations];
      activeId = conversation.id;
      editingId = '';
      pendingImages = [];
      historyOpen = false;
      await persist();
      if (!silent) {
        render();
        input.focus();
      }
      return active() || conversation;
    }

    // --- composer -----------------------------------------------------------

    function resizeInput() {
      input.style.height = 'auto';
      input.style.height = `${Math.min(160, input.scrollHeight)}px`;
    }

    composer.addEventListener('submit', (event) => {
      event.preventDefault();
      submit();
    });
    sendBtn.addEventListener('click', (event) => {
      event.preventDefault();
      if (busy) {
        if (cancelTurn) cancelTurn();
        return;
      }
      submit();
    });
    input.addEventListener('input', resizeInput);
    fileInput.addEventListener('change', () => {
      const files = Array.from(fileInput.files || []);
      fileInput.value = '';
      addFiles(files);
    });
    input.addEventListener('paste', (event) => {
      const files = Array.from((event.clipboardData && event.clipboardData.files) || []);
      if (!files.length) return;
      event.preventDefault();
      addFiles(files);
    });
    ['dragenter', 'dragover'].forEach((type) => {
      main.addEventListener(type, (event) => {
        if (!carriesFiles(event)) return;
        event.preventDefault();
        if (event.dataTransfer) event.dataTransfer.dropEffect = 'copy';
        setDragging(true);
      });
    });
    main.addEventListener('dragleave', (event) => {
      if (event.target === main || !main.contains(event.relatedTarget)) setDragging(false);
    });
    main.addEventListener('drop', (event) => {
      if (!carriesFiles(event)) return;
      event.preventDefault();
      setDragging(false);
      addFiles(event.dataTransfer.files);
    });
    input.addEventListener('keydown', (event) => {
      if (event.key !== 'Enter' || event.shiftKey || event.isComposing) return;
      event.preventDefault();
      submit();
    });

    // --- boot -----------------------------------------------------------------

    async function readStatus() {
      const status = await getStatus();
      configured = !!(status && status.configured);
      visionEnabled = !!(status && status.vision);
      streamingEnabled = !(status && status.streaming === false);
      if (!visionEnabled && pendingImages.length) pendingImages = [];
    }

    async function start() {
      await readStatus();
      conversations = await Store.load(storage);
      lastWritten = JSON.stringify(conversations);
      if (conversations[0]) activeId = conversations[0].id;
      else await startNewConversation({ silent: true });
      render();
      resizeInput();
      scrollToEnd();
    }

    // Cross-tab consistency, when the storage adapter can offer it: another
    // window editing the same history should not stomp a turn in flight, and
    // this tab's own writes are recognised rather than re-applied.
    if (typeof storage.onChanged === 'function') {
      storage.onChanged((changes, area) => {
        if (area && area !== 'local') return;
        const change = changes && changes[Store.STORAGE_KEY];
        if (!change || busy) return;
        const incoming = JSON.stringify(Store.normalizeConversations(change.newValue));
        if (incoming === lastWritten) return;
        Store.load(storage).then((list) => {
          conversations = list;
          lastWritten = JSON.stringify(list);
          if (!active() && list.length) activeId = list[0].id;
          render();
        }).catch(() => {});
      });
    }

    const ready = start().catch((error) => {
      setFlash(String((error && error.message) || error));
    });

    return {
      ready,
      render,
      focus: () => input.focus({ preventScroll: true }),
      newConversation: () => startNewConversation(),
      refreshStatus: () => readStatus().then(render),
      // Toggles the narrow-screen overlay sidebar. Exposed because the wide
      // skin hides this component's own bar (see chat.css) in favour of a
      // host-supplied header — see src/workspace.js's rail toggle button.
      toggleHistory: () => {
        historyOpen = !historyOpen;
        render();
      }
    };
  }

  return Object.freeze({ STRINGS, mount });
});
