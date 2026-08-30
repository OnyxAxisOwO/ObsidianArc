// The ready-to-use shell around src/chat.js: a header bar (rail toggle, model
// picker, theme toggle, settings) and a settings drawer (provider, API key,
// model shortlist with detection, advanced request tuning), all wired to
// src/provider.js.
//
// src/chat.js itself has no idea providers exist — it only knows about
// getStatus()/send() — so everything provider-specific lives here. A host
// that wants a different backend (its own server, a browser extension's
// background page) can skip this file entirely and mount src/chat.js
// directly with its own adapters.
//
// Builds its own DOM (like chat.js does) rather than expecting a host page to
// hand-author specific element ids, so mounting it is: give it an empty
// container and a storage adapter.

(function (root, factory) {
  const api = factory();
  if (typeof module === 'object' && module.exports) module.exports = api;
  if (root) root.ObsidianWorkspace = api;
})(typeof globalThis !== 'undefined' ? globalThis : this, function () {
  'use strict';

  const SVG_NS = 'http://www.w3.org/2000/svg';
  const RAIL_COLLAPSED_KEY = 'obsidian-arc-rail-collapsed';
  const THEME_KEY = 'obsidian-arc-theme';

  const ICONS = {
    menu: ['M3 6h18', 'M3 12h18', 'M3 18h18'],
    gear: [
      'M12 15a3 3 0 1 0 0-6 3 3 0 0 0 0 6Z',
      'M19.4 15a1.65 1.65 0 0 0 .33 1.82l.06.06a2 2 0 1 1-2.83 2.83l-.06-.06a1.65 1.65 0 0 0-1.82-.33 1.65 1.65 0 0 0-1 1.51V21a2 2 0 0 1-4 0v-.09a1.65 1.65 0 0 0-1.08-1.51 1.65 1.65 0 0 0-1.82.33l-.06.06a2 2 0 1 1-2.83-2.83l.06-.06a1.65 1.65 0 0 0 .33-1.82 1.65 1.65 0 0 0-1.51-1H3a2 2 0 0 1 0-4h.09a1.65 1.65 0 0 0 1.51-1.08 1.65 1.65 0 0 0-.33-1.82l-.06-.06a2 2 0 1 1 2.83-2.83l.06.06a1.65 1.65 0 0 0 1.82.33H9a1.65 1.65 0 0 0 1-1.51V3a2 2 0 0 1 4 0v.09a1.65 1.65 0 0 0 1 1.51 1.65 1.65 0 0 0 1.82-.33l.06-.06a2 2 0 1 1 2.83 2.83l-.06.06a1.65 1.65 0 0 0-.33 1.82V9a1.65 1.65 0 0 0 1.51 1H21a2 2 0 0 1 0 4h-.09a1.65 1.65 0 0 0-1.51 1Z'
    ],
    chevron: ['m6 9 6 6 6-6'],
    sun: ['M12 17a5 5 0 1 0 0-10 5 5 0 0 0 0 10Z', 'M12 1v2', 'M12 21v2', 'M4.22 4.22l1.42 1.42', 'M18.36 18.36l1.42 1.42', 'M1 12h2', 'M21 12h2', 'M4.22 19.78l1.42-1.42', 'M18.36 5.64l1.42-1.42'],
    moon: ['M21 12.79A9 9 0 1 1 11.21 3 7 7 0 0 0 21 12.79Z'],
    auto: ['M3 5h18v11H3z', 'M8 20h8', 'M12 16v4'],
    close: ['M18 6L6 18', 'M6 6l12 12'],
    eye: ['M1 12s4-7 11-7 11 7 11 7-4 7-11 7-11-7-11-7Z', 'M12 15a3 3 0 1 0 0-6 3 3 0 0 0 0 6Z'],
    eyeOff: ['M17.94 17.94A10.94 10.94 0 0 1 12 19c-7 0-11-7-11-7a18.4 18.4 0 0 1 4.22-5.06', 'M9.9 4.24A9.12 9.12 0 0 1 12 4c7 0 11 7 11 7a18.5 18.5 0 0 1-2.16 3.19', 'M14.12 14.12a3 3 0 1 1-4.24-4.24', 'M1 1l22 22']
  };

  const STRINGS = {
    en: {
      brand: 'Obsidian Arc',
      railToggle: 'Conversation list',
      settings: 'Settings',
      theme: 'Theme',
      themeLight: 'Light', themeDark: 'Dark', themeAuto: 'Match system',
      modelNone: 'Choose a model',
      manageModels: 'Manage models…',
      settingsTitle: 'AI settings',
      close: 'Close',
      provider: 'Provider',
      apiKey: 'API key',
      apiKeyPlaceholder: 'Paste your API key',
      showKey: 'Show key', hideKey: 'Hide key',
      encryptKey: 'Encrypt the key at rest',
      baseUrl: 'Base URL (optional)',
      vision: 'This model can read images',
      streaming: 'Stream responses',
      systemPrompt: 'Custom instructions',
      systemPromptPlaceholder: 'Applied to every conversation, e.g. "answer concisely" or "write in French".',
      models: 'Model shortlist',
      modelsEmpty: 'No models saved yet.',
      modelIdPlaceholder: 'model id',
      modelLabelPlaceholder: 'Nickname (optional)',
      addModel: 'Add',
      useModel: 'Use this model',
      removeModel: 'Remove',
      detect: 'Detect available models',
      detecting: 'Asking the endpoint…',
      detected: '{count} models found.',
      detectAdd: 'Add selected',
      detectCancel: 'Cancel',
      alreadySaved: 'saved',
      advanced: 'Advanced',
      temperature: 'Temperature',
      maxTokens: 'Max output tokens',
      extendedThinking: 'Extended thinking (Anthropic)',
      thinkingBudget: 'Thinking budget (tokens)',
      extraBody: 'Extra request fields (JSON)',
      extraBodyInvalid: 'Not valid JSON — ignored.',
      done: 'Done'
    },
    zh: {
      brand: 'Obsidian Arc',
      railToggle: '对话列表',
      settings: '设置',
      theme: '主题',
      themeLight: '浅色', themeDark: '深色', themeAuto: '跟随系统',
      modelNone: '选择模型',
      manageModels: '管理模型…',
      settingsTitle: 'AI 设置',
      close: '关闭',
      provider: '服务商',
      apiKey: 'API 密钥',
      apiKeyPlaceholder: '粘贴你的 API 密钥',
      showKey: '显示密钥', hideKey: '隐藏密钥',
      encryptKey: '密钥加密存储',
      baseUrl: 'Base URL（可选）',
      vision: '这个模型能看图',
      streaming: '流式返回',
      systemPrompt: '自定义指令',
      systemPromptPlaceholder: '应用到每个对话，例如“回答尽量简洁”或“用中文回答”。',
      models: '模型列表',
      modelsEmpty: '还没有保存任何模型。',
      modelIdPlaceholder: '模型 ID',
      modelLabelPlaceholder: '昵称（可选）',
      addModel: '添加',
      useModel: '使用这个模型',
      removeModel: '移除',
      detect: '检测可用模型',
      detecting: '正在询问接口…',
      detected: '找到 {count} 个模型。',
      detectAdd: '添加选中项',
      detectCancel: '取消',
      alreadySaved: '已保存',
      advanced: '高级选项',
      temperature: 'Temperature',
      maxTokens: '最大输出 tokens',
      extendedThinking: '扩展思考（Anthropic）',
      thinkingBudget: '思考预算（tokens）',
      extraBody: '额外请求字段（JSON）',
      extraBodyInvalid: '不是合法 JSON，已忽略。',
      done: '完成'
    }
  };

  function mount(root, options) {
    if (!root) throw new Error('ObsidianWorkspace.mount needs a root element.');
    const opts = options || {};
    const doc = root.ownerDocument;
    const Provider = opts.provider || (typeof globalThis !== 'undefined' ? globalThis.ObsidianProvider : null);
    const storage = opts.storage;
    if (!Provider || !storage) throw new Error('ObsidianWorkspace.mount needs { provider, storage }.');
    const lang = STRINGS[opts.lang] ? opts.lang : 'en';

    function str(key, vars) {
      const value = (STRINGS[lang] && STRINGS[lang][key]) || STRINGS.en[key] || key;
      return vars ? value.replace(/\{(\w+)\}/g, (m, name) => (name in vars ? String(vars[name]) : m)) : value;
    }

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

    let config = Provider.normalizeConfig(null);
    // Assigned once src/chat.js is mounted, below. The rail toggle is built
    // first (it belongs at the start of the header) but can't fire before a
    // user clicks it, so the forward reference is safe.
    let chatHandle = null;

    // --- layout ---------------------------------------------------------------

    root.classList.add('oa-workspace');
    root.textContent = '';

    const header = el('div', 'oa-header');
    // Two different affordances share one button: on a wide screen the rail
    // is a permanent column and this slides it away; below the breakpoint
    // where chat.css turns it into an overlay (see .ai-chat-wide's mobile
    // rules), sliding it away would do nothing useful, so this defers to
    // src/chat.js's own overlay toggle instead.
    const railToggle = button('oa-icon-btn', '', () => {
      if (doc.defaultView.matchMedia('(max-width: 900px)').matches) {
        if (chatHandle) chatHandle.toggleHistory();
        return;
      }
      const collapsed = chatRoot.classList.toggle('rail-collapsed');
      try { localStorage.setItem(RAIL_COLLAPSED_KEY, collapsed ? '1' : ''); } catch (_) { /* best-effort */ }
    });
    railToggle.appendChild(icon(ICONS.menu, 17));
    railToggle.title = str('railToggle');
    railToggle.setAttribute('aria-label', str('railToggle'));

    const brand = el('span', 'oa-brand', str('brand'));

    const modelChip = button('oa-chip', '');
    const modelChipLabel = el('span', 'oa-chip-label', str('modelNone'));
    modelChip.appendChild(modelChipLabel);
    modelChip.appendChild(icon(ICONS.chevron, 13));
    const modelMenu = el('div', 'oa-menu');
    modelMenu.hidden = true;

    const themeBtn = button('oa-icon-btn', '');
    themeBtn.title = str('theme');
    themeBtn.setAttribute('aria-label', str('theme'));

    const settingsBtn = button('oa-icon-btn', '', () => openDrawer());
    settingsBtn.appendChild(icon(ICONS.gear, 17));
    settingsBtn.title = str('settings');
    settingsBtn.setAttribute('aria-label', str('settings'));

    const chipGroup = el('div', 'oa-chip-group');
    chipGroup.appendChild(modelChip);
    chipGroup.appendChild(modelMenu);

    header.appendChild(railToggle);
    header.appendChild(brand);
    header.appendChild(el('span', 'oa-header-spacer'));
    header.appendChild(chipGroup);
    header.appendChild(themeBtn);
    header.appendChild(settingsBtn);

    const chatRoot = el('div', 'oa-chat-root');
    root.appendChild(header);
    root.appendChild(chatRoot);

    // --- theme (light/dark/auto) ------------------------------------------

    function currentTheme() {
      try { return localStorage.getItem(THEME_KEY) || 'auto'; } catch (_) { return 'auto'; }
    }
    function applyTheme(mode) {
      const docEl = doc.documentElement;
      if (mode === 'dark') docEl.setAttribute('data-theme', 'dark');
      else if (mode === 'light') docEl.setAttribute('data-theme', 'light');
      else docEl.removeAttribute('data-theme');
      themeBtn.textContent = '';
      themeBtn.appendChild(icon(mode === 'dark' ? ICONS.moon : (mode === 'light' ? ICONS.sun : ICONS.auto), 17));
    }
    themeBtn.addEventListener('click', () => {
      const next = { auto: 'light', light: 'dark', dark: 'auto' }[currentTheme()] || 'auto';
      try { localStorage.setItem(THEME_KEY, next); } catch (_) { /* best-effort */ }
      applyTheme(next);
    });
    applyTheme(currentTheme());

    try {
      if (localStorage.getItem(RAIL_COLLAPSED_KEY) === '1') chatRoot.classList.add('rail-collapsed');
    } catch (_) { /* best-effort */ }

    // --- chip menus -------------------------------------------------------

    function closeModelMenu() {
      modelMenu.hidden = true;
      modelChip.setAttribute('aria-expanded', 'false');
    }
    doc.addEventListener('click', (event) => {
      if (!event.target.closest('.oa-chip-group')) closeModelMenu();
    });
    doc.addEventListener('keydown', (event) => {
      if (event.key === 'Escape') closeModelMenu();
    });

    function paintModelChip() {
      const label = Provider.modelLabel(config, config.model);
      modelChipLabel.textContent = label || str('modelNone');
      modelChip.classList.toggle('placeholder', !label);
    }

    function openModelMenu() {
      modelMenu.textContent = '';
      config.models.forEach((entry) => {
        const item = button('oa-menu-item', '', () => {
          closeModelMenu();
          persistConfig({ model: entry.id });
        });
        if (entry.id === config.model) item.classList.add('active');
        item.appendChild(el('span', 'oa-menu-item-title', entry.label || entry.id));
        if (entry.label) item.appendChild(el('span', 'oa-menu-item-sub', entry.id));
        modelMenu.appendChild(item);
      });
      if (!config.models.length) modelMenu.appendChild(el('p', 'oa-menu-empty', str('modelsEmpty')));
      const manage = button('oa-menu-item oa-menu-manage', str('manageModels'), () => {
        closeModelMenu();
        openDrawer();
      });
      modelMenu.appendChild(manage);
      modelMenu.hidden = false;
      modelChip.setAttribute('aria-expanded', 'true');
    }
    modelChip.addEventListener('click', () => {
      if (!modelMenu.hidden) { closeModelMenu(); return; }
      openModelMenu();
    });

    // --- settings drawer ----------------------------------------------------

    const overlay = el('div', 'oa-drawer-overlay');
    overlay.hidden = true;
    const drawer = el('aside', 'oa-drawer');
    drawer.hidden = true;
    const drawerHead = el('div', 'oa-drawer-head');
    drawerHead.appendChild(el('h2', 'oa-drawer-title', str('settingsTitle')));
    const drawerClose = button('oa-icon-btn', '', () => closeDrawer());
    drawerClose.appendChild(icon(ICONS.close, 16));
    drawerClose.title = str('close');
    drawerClose.setAttribute('aria-label', str('close'));
    drawerHead.appendChild(drawerClose);
    const drawerBody = el('div', 'oa-drawer-body');
    drawer.appendChild(drawerHead);
    drawer.appendChild(drawerBody);
    overlay.addEventListener('click', () => closeDrawer());

    function openDrawer() {
      overlay.hidden = false;
      drawer.hidden = false;
      requestAnimationFrame(() => { overlay.classList.add('open'); drawer.classList.add('open'); });
    }
    function closeDrawer() {
      overlay.classList.remove('open');
      drawer.classList.remove('open');
      setTimeout(() => { overlay.hidden = true; drawer.hidden = true; }, 200);
    }

    function field(labelText, node, hint) {
      const wrap = el('label', 'oa-field');
      wrap.appendChild(el('span', 'oa-field-label', labelText));
      wrap.appendChild(node);
      if (hint) wrap.appendChild(el('span', 'oa-field-hint', hint));
      return wrap;
    }
    function checkboxField(labelText, checked, onChange) {
      const wrap = el('label', 'oa-checkbox-field');
      const box = el('input');
      box.type = 'checkbox';
      box.checked = !!checked;
      box.addEventListener('change', () => onChange(box.checked));
      wrap.appendChild(box);
      wrap.appendChild(el('span', null, labelText));
      return { wrap, box };
    }

    let writeChain = Promise.resolve();
    function persistConfig(partial) {
      writeChain = writeChain.catch(() => {}).then(async () => {
        config = await Provider.saveConfig(partial, storage);
        paintModelChip();
        if (chatHandle) await chatHandle.refreshStatus();
      });
      return writeChain;
    }

    // Provider select
    const providerSelect = doc.createElement('select');
    Object.values(Provider.PROVIDERS).forEach((preset) => {
      const opt = doc.createElement('option');
      opt.value = preset.id;
      opt.textContent = preset.label;
      providerSelect.appendChild(opt);
    });
    providerSelect.addEventListener('change', () => persistConfig({ provider: providerSelect.value, model: '' }));

    // API key
    const apiKeyRow = el('div', 'oa-input-row');
    const apiKeyInput = doc.createElement('input');
    apiKeyInput.type = 'password';
    apiKeyInput.placeholder = str('apiKeyPlaceholder');
    apiKeyInput.autocomplete = 'off';
    apiKeyInput.spellcheck = false;
    const apiKeyToggle = button('oa-icon-btn', '', () => {
      const show = apiKeyInput.type === 'password';
      apiKeyInput.type = show ? 'text' : 'password';
      apiKeyToggle.textContent = '';
      apiKeyToggle.appendChild(icon(show ? ICONS.eyeOff : ICONS.eye, 16));
      apiKeyToggle.title = str(show ? 'hideKey' : 'showKey');
    });
    apiKeyToggle.appendChild(icon(ICONS.eye, 16));
    apiKeyToggle.title = str('showKey');
    apiKeyInput.addEventListener('change', () => persistConfig({ apiKey: apiKeyInput.value.trim() }));
    apiKeyRow.appendChild(apiKeyInput);
    apiKeyRow.appendChild(apiKeyToggle);
    const encryptCheck = checkboxField(str('encryptKey'), false, (checked) => persistConfig({ encryptApiKey: checked }));

    // Base URL
    const baseUrlInput = doc.createElement('input');
    baseUrlInput.type = 'text';
    baseUrlInput.spellcheck = false;
    baseUrlInput.addEventListener('change', () => {
      try {
        const normalized = baseUrlInput.value.trim() ? Provider.normalizeBaseUrl(baseUrlInput.value, '') : '';
        persistConfig({ baseUrl: normalized });
      } catch (error) {
        setDrawerFlash(String((error && error.message) || error));
      }
    });

    const drawerFlash = el('p', 'oa-drawer-flash');
    function setDrawerFlash(text) {
      drawerFlash.textContent = text || '';
      drawerFlash.classList.toggle('visible', !!text);
    }

    const visionCheck = checkboxField(str('vision'), false, (checked) => persistConfig({ vision: checked }));
    const streamingCheck = checkboxField(str('streaming'), true, (checked) => persistConfig({ streaming: checked }));

    const systemPromptInput = doc.createElement('textarea');
    systemPromptInput.rows = 3;
    systemPromptInput.placeholder = str('systemPromptPlaceholder');
    systemPromptInput.addEventListener('change', () => persistConfig({ systemPrompt: systemPromptInput.value }));

    // Model shortlist
    const modelListEl = el('div', 'oa-model-list');
    const modelAddId = doc.createElement('input');
    modelAddId.type = 'text';
    modelAddId.placeholder = str('modelIdPlaceholder');
    modelAddId.spellcheck = false;
    const modelAddLabel = doc.createElement('input');
    modelAddLabel.type = 'text';
    modelAddLabel.placeholder = str('modelLabelPlaceholder');
    const modelAddRow = el('div', 'oa-input-row');
    const modelAddBtn = button('oa-btn', str('addModel'), () => {
      const id = modelAddId.value.trim();
      if (!id || config.models.some((m) => m.id === id)) return;
      const label = modelAddLabel.value.trim();
      persistConfig({ models: config.models.concat([label ? { id, label } : { id }]) });
      modelAddId.value = '';
      modelAddLabel.value = '';
    });
    modelAddRow.appendChild(modelAddId);
    modelAddRow.appendChild(modelAddLabel);
    modelAddRow.appendChild(modelAddBtn);

    function renderModelList() {
      modelListEl.textContent = '';
      if (!config.models.length) {
        modelListEl.appendChild(el('p', 'oa-menu-empty', str('modelsEmpty')));
        return;
      }
      config.models.forEach((entry) => {
        const row = el('div', `oa-model-row${entry.id === config.model ? ' active' : ''}`);
        const use = button('oa-icon-btn', '', () => persistConfig({ model: entry.id }));
        use.appendChild(icon(['M20 6 9 17l-5-5'], 14));
        use.title = str('useModel');
        use.setAttribute('aria-label', str('useModel'));
        const info = el('div', 'oa-model-row-info');
        info.appendChild(el('span', 'oa-model-row-title', entry.label || entry.id));
        if (entry.label) info.appendChild(el('span', 'oa-model-row-sub', entry.id));
        const remove = button('oa-icon-btn', '', () => persistConfig({ models: config.models.filter((m) => m.id !== entry.id) }));
        remove.appendChild(icon(ICONS.close, 14));
        remove.title = str('removeModel');
        remove.setAttribute('aria-label', str('removeModel'));
        row.appendChild(use);
        row.appendChild(info);
        row.appendChild(remove);
        modelListEl.appendChild(row);
      });
    }

    // Detect models
    const detectBtn = button('oa-btn', str('detect'), () => detectModels());
    const detectPanel = el('div', 'oa-detect-panel');
    detectPanel.hidden = true;
    const detectStatus = el('p', 'oa-detect-status');
    const detectList = el('div', 'oa-detect-list');
    const detectActions = el('div', 'oa-detect-actions');
    detectActions.hidden = true;
    const detectAddBtn = button('oa-btn primary', str('detectAdd'));
    const detectCancelBtn = button('oa-btn', str('detectCancel'), () => { detectPanel.hidden = true; });
    detectActions.appendChild(detectCancelBtn);
    detectActions.appendChild(detectAddBtn);
    detectPanel.appendChild(detectStatus);
    detectPanel.appendChild(detectList);
    detectPanel.appendChild(detectActions);

    let detected = [];
    async function detectModels() {
      detectPanel.hidden = false;
      detectActions.hidden = true;
      detectList.textContent = '';
      detectStatus.textContent = str('detecting');
      detectBtn.disabled = true;
      try {
        detected = await Provider.listModels(config);
        detectStatus.textContent = str('detected', { count: detected.length });
        detected.forEach((entry, index) => {
          const row = el('label', 'oa-detect-row');
          const box = doc.createElement('input');
          box.type = 'checkbox';
          box.dataset.index = String(index);
          const known = config.models.some((m) => m.id === entry.id);
          box.disabled = known;
          row.appendChild(box);
          const name = el('span', null, entry.label && entry.label !== entry.id ? `${entry.label} — ${entry.id}` : entry.id);
          row.appendChild(name);
          if (known) row.appendChild(el('span', 'oa-detect-known', str('alreadySaved')));
          detectList.appendChild(row);
        });
        detectActions.hidden = false;
      } catch (error) {
        detectStatus.textContent = String((error && error.message) || error);
      } finally {
        detectBtn.disabled = false;
      }
    }
    detectAddBtn.addEventListener('click', () => {
      const picked = [...detectList.querySelectorAll('input:checked')].map((box) => detected[Number(box.dataset.index)]).filter(Boolean);
      if (picked.length) {
        const merged = config.models.slice();
        picked.forEach((entry) => { if (!merged.some((m) => m.id === entry.id)) merged.push(entry); });
        persistConfig({ models: merged });
      }
      detectPanel.hidden = true;
    });

    // Advanced
    const advanced = doc.createElement('details');
    advanced.className = 'oa-advanced';
    const advancedSummary = doc.createElement('summary');
    advancedSummary.textContent = str('advanced');
    advanced.appendChild(advancedSummary);

    const temperatureInput = doc.createElement('input');
    temperatureInput.type = 'number';
    temperatureInput.min = '0';
    temperatureInput.max = '2';
    temperatureInput.step = '0.1';
    temperatureInput.addEventListener('change', () => {
      const value = temperatureInput.value.trim();
      persistConfig({ temperature: value === '' ? null : Number(value) });
    });

    const maxTokensInput = doc.createElement('input');
    maxTokensInput.type = 'number';
    maxTokensInput.min = '1';
    maxTokensInput.addEventListener('change', () => {
      const value = maxTokensInput.value.trim();
      persistConfig({ maxTokens: value === '' ? null : Number(value) });
    });

    const thinkingCheck = checkboxField(str('extendedThinking'), false, (checked) => persistConfig({ extendedThinking: checked }));
    const thinkingBudgetInput = doc.createElement('input');
    thinkingBudgetInput.type = 'number';
    thinkingBudgetInput.min = '1024';
    thinkingBudgetInput.addEventListener('change', () => persistConfig({ thinkingBudget: Number(thinkingBudgetInput.value) || 4096 }));

    const extraBodyInput = doc.createElement('textarea');
    extraBodyInput.rows = 3;
    extraBodyInput.spellcheck = false;
    extraBodyInput.placeholder = '{\n  "top_p": 0.9\n}';
    extraBodyInput.addEventListener('change', () => {
      if (!Provider.extraBodyLooksValid(extraBodyInput.value)) setDrawerFlash(str('extraBodyInvalid'));
      else setDrawerFlash('');
      persistConfig({ extraBody: extraBodyInput.value });
    });

    advanced.appendChild(field(str('temperature'), temperatureInput));
    advanced.appendChild(field(str('maxTokens'), maxTokensInput));
    advanced.appendChild(thinkingCheck.wrap);
    advanced.appendChild(field(str('thinkingBudget'), thinkingBudgetInput));
    advanced.appendChild(field(str('extraBody'), extraBodyInput));

    drawerBody.appendChild(field(str('provider'), providerSelect));
    drawerBody.appendChild(field(str('apiKey'), apiKeyRow));
    drawerBody.appendChild(encryptCheck.wrap);
    drawerBody.appendChild(field(str('baseUrl'), baseUrlInput));
    drawerBody.appendChild(visionCheck.wrap);
    drawerBody.appendChild(streamingCheck.wrap);
    drawerBody.appendChild(field(str('systemPrompt'), systemPromptInput));
    drawerBody.appendChild(drawerFlash);
    drawerBody.appendChild(el('h3', 'oa-drawer-subhead', str('models')));
    drawerBody.appendChild(modelListEl);
    drawerBody.appendChild(modelAddRow);
    drawerBody.appendChild(detectBtn);
    drawerBody.appendChild(detectPanel);
    drawerBody.appendChild(advanced);

    root.appendChild(overlay);
    root.appendChild(drawer);

    function paintDrawer() {
      providerSelect.value = config.provider;
      apiKeyInput.value = config.apiKey;
      encryptCheck.box.checked = config.encryptApiKey;
      baseUrlInput.value = config.baseUrl;
      baseUrlInput.placeholder = Provider.PROVIDERS[config.provider].defaultBaseUrl;
      visionCheck.box.checked = config.vision;
      streamingCheck.box.checked = config.streaming;
      systemPromptInput.value = config.systemPrompt;
      temperatureInput.value = config.temperature === null ? '' : String(config.temperature);
      maxTokensInput.value = config.maxTokens === null ? '' : String(config.maxTokens);
      thinkingCheck.box.checked = config.extendedThinking;
      thinkingBudgetInput.value = String(config.thinkingBudget);
      extraBodyInput.value = config.extraBody;
      renderModelList();
      paintModelChip();
    }

    // --- adapters for src/chat.js -------------------------------------------

    async function getStatus() {
      return { configured: !!(config.apiKey && config.model), vision: config.vision === true, streaming: config.streaming !== false };
    }

    async function send({ turns, signal, onReply, onThinking }) {
      return Provider.chatStream({ config, turns, signal, onReply, onThinking });
    }

    if (!globalThis.ObsidianChat) throw new Error('ObsidianWorkspace.mount needs src/chat.js loaded first.');
    chatHandle = globalThis.ObsidianChat.mount({
      root: chatRoot,
      storage,
      store: opts.store,
      markdown: opts.markdown,
      image: opts.image,
      variant: 'wide',
      lang,
      mainHeader: null,
      getStatus,
      send,
      openSettings: openDrawer
    });

    async function boot() {
      config = await Provider.loadConfig(storage);
      paintDrawer();
      // src/chat.js's own start() reads getStatus() the moment mount()
      // returns, which races this function's async storage read — on a
      // fresh page load, that first check almost always loses and sees the
      // pre-load default (unconfigured) config. Re-checking once the real
      // config is in hand is what makes an already-configured reload open
      // straight into the chat instead of a stale setup card.
      if (chatHandle) await chatHandle.refreshStatus();
    }
    if (typeof storage.onChanged === 'function') {
      storage.onChanged((changes, area) => {
        if (area && area !== 'local') return;
        if (changes && Object.prototype.hasOwnProperty.call(changes, '__obsidian_arc_ai_config__')) {
          boot();
        }
      });
    }
    const ready = boot();

    return { ready, openSettings: openDrawer, closeSettings: closeDrawer, chat: chatHandle };
  }

  return Object.freeze({ mount });
});
