// The chat surface: rail, transcript, composer, streaming, editing,
// attachments.
//
// Ported from the standalone build's chat.js with its structure, class names,
// animations and interactions intact — the transcript is still rebuilt on
// render() with a fast path that patches the streaming message in place, the
// rail still slides, the caret still blinks at the live end of an answer.
//
// What changed is where the conversation lives. It used to be an array in
// localStorage that was re-sent whole on every turn; it is now rows on the
// server, and a turn sends an intent — "answer this, after rewinding to
// there" — instead of a transcript. The component still holds a local copy so
// rendering stays synchronous, and still treats the visible transcript as the
// truth for what to draw.

import {
  attachmentURL,
  deleteAllConversations,
  deleteConversation,
  getConversation,
  listConversations,
  renameConversation,
  sendTurn,
  uploadAttachment,
  type AttachmentRef,
  type Conversation,
  type Message,
  type MessageStats,
} from '../api/chat';
import { ApiError } from '../api/client';
import { t } from '../i18n';
import { ICONS, button, clear, el, icon, iconButton } from '../ui/dom';
import { createComposerMenu, type ComposerMenu, type ReasoningState } from './composer-menu';
import { ImageError, prepareImage, type PreparedImage } from './image';
import { renderInto } from './markdown';

const MAX_MESSAGE_CHARS = 32000;
const MAX_IMAGES = 6;
const SUGGESTIONS_SHOWN = 3;

// A text file becomes part of the message rather than an attachment: both
// provider protocols take images and text, and nothing else, so a document
// that a model can read as characters belongs in the prompt. Anything else —
// a PDF, a spreadsheet — would have to be converted server-side, which is a
// feature and not a file picker.
const MAX_FILE_BYTES = 256 * 1024;
const MAX_FILE_CHARS = 24000;
const TEXT_FILE_ACCEPT = [
  '.txt', '.md', '.markdown', '.csv', '.tsv', '.json', '.jsonl', '.yaml', '.yml',
  '.toml', '.ini', '.cfg', '.conf', '.env', '.log', '.xml', '.html', '.css',
  '.js', '.jsx', '.ts', '.tsx', '.go', '.rs', '.py', '.rb', '.java', '.kt',
  '.c', '.h', '.cc', '.cpp', '.hpp', '.cs', '.php', '.swift', '.sh', '.bash',
  '.zsh', '.ps1', '.sql', '.graphql', '.proto', '.dockerfile', '.gitignore',
  'text/*',
].join(',');

// The fence language, so a code file arrives highlighted rather than as a
// wall of prose.
function fenceLanguage(name: string): string {
  const extension = name.slice(name.lastIndexOf('.') + 1).toLowerCase();
  const known: Record<string, string> = {
    ts: 'ts', tsx: 'tsx', js: 'js', jsx: 'jsx', go: 'go', rs: 'rust', py: 'python',
    rb: 'ruby', java: 'java', kt: 'kotlin', c: 'c', h: 'c', cc: 'cpp', cpp: 'cpp',
    hpp: 'cpp', cs: 'csharp', php: 'php', swift: 'swift', sh: 'bash', bash: 'bash',
    zsh: 'bash', ps1: 'powershell', sql: 'sql', json: 'json', jsonl: 'json',
    yaml: 'yaml', yml: 'yaml', toml: 'toml', xml: 'xml', html: 'html', css: 'css',
    md: 'markdown', markdown: 'markdown', csv: 'csv', tsv: 'csv',
  };
  return known[extension] ?? '';
}

// Far more suggestions than fit on screen, so each visit offers a different
// three. A fixed trio is read once and then becomes furniture; a rotating one
// keeps suggesting things the user did not know to ask for.
const SUGGESTION_KEYS = [
  'suggestionExplain', 'suggestionBrainstorm', 'suggestionWrite', 'suggestionSummarize',
  'suggestionDebug', 'suggestionPlan', 'suggestionCompare', 'suggestionRewrite',
  'suggestionLearn', 'suggestionDecide', 'suggestionDraft', 'suggestionCritique',
] as const;

export interface ChatStatus {
  /** At least one model is available to this account. */
  configured: boolean;
  /** The selected model accepts images. */
  vision: boolean;
  reasoningAvailable: boolean;
  reasoningEnabled: boolean;
  reasoningEffort: string;
  modelID: string;
  /** True for an administrator, who is offered the setup shortcut. */
  canAdminister: boolean;
}

export interface ChatOptions {
  root: HTMLElement;
  getStatus(): ChatStatus;
  onOpenSetup(): void;
  /** Applied to the next turn, and persisted by the host. */
  onReasoningChange(next: ReasoningState): void;
  /** Told which conversation is open, so the shell can reflect it. */
  onConversationChange?(conversation: Conversation | null): void;
}

export interface ChatHandle {
  ready: Promise<void>;
  refreshStatus(): void;
  newConversation(): void;
  toggleHistory(): void;
  focus(): void;
  destroy(): void;
}

/** A message being streamed, before it becomes a row. */
interface Pending {
  answer: string;
  reasoning: string;
}

export function mountChat(options: ChatOptions): ChatHandle {
  const host = options.root;
  host.classList.add('ai-chat', 'ai-chat-wide');
  clear(host);

  // --- state ------------------------------------------------------------------

  let conversations: Conversation[] = [];
  let activeID = '';
  let messages: Message[] = [];
  let status = options.getStatus();

  let busy = false;
  let controller: AbortController | null = null;
  let editingID = '';
  let justSentID = '';
  let historyOpen = false;
  let flash = '';
  let pending: Pending | null = null;
  let pendingNodes: { reasoning: HTMLElement | null; answer: HTMLElement | null } | null = null;
  let lastRenderedID: string | null = null;
  let destroyed = false;

  let attachments: Array<{ ref: AttachmentRef; preview: string }> = [];

  const shownSuggestions = pickSuggestions(SUGGESTIONS_SHOWN);

  // --- structure --------------------------------------------------------------

  const sidebar = el('div', 'ai-chat-sidebar');
  const sidebarHead = el('div', 'ai-chat-sidebar-head');
  const newChatBtn = button('ai-chat-new', '', () => startNewConversation());
  newChatBtn.appendChild(icon(ICONS.plus, 14));
  newChatBtn.appendChild(el('span', null, t('newChat')));
  sidebarHead.appendChild(el('span', 'ai-chat-sidebar-title', t('history')));
  sidebarHead.appendChild(newChatBtn);

  const sidebarList = el('div', 'ai-chat-list');
  const sidebarFoot = el('div', 'ai-chat-sidebar-foot');
  const clearAllBtn = button('ai-chat-clear-all', '', () => void clearEverything());
  clearAllBtn.appendChild(icon(ICONS.trash, 12));
  clearAllBtn.appendChild(el('span', null, t('clearAll')));
  sidebarFoot.appendChild(clearAllBtn);

  sidebar.appendChild(sidebarHead);
  sidebar.appendChild(sidebarList);
  sidebar.appendChild(sidebarFoot);

  const main = el('div', 'ai-chat-main');

  // The wide skin hides this bar in favour of the workspace header; it is the
  // navigation on a narrow screen, where the rail is an overlay.
  const bar = el('div', 'ai-chat-bar');
  const historyBtn = iconButton('ai-chat-bar-btn', ICONS.menu, t('history'), () => {
    historyOpen = !historyOpen;
    render();
  });
  const barTitle = el('span', 'ai-chat-bar-title', t('brand'));
  const barNewBtn = iconButton('ai-chat-bar-btn', ICONS.plus, t('newChat'), () => startNewConversation(), 15);
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

  // Text and code only. Anything a model can read as characters is pasted
  // into the message; a PDF or a spreadsheet is not something either provider
  // protocol accepts, and pretending otherwise would fail at the far end.
  const docInput = el('input', 'ai-chat-file');
  docInput.type = 'file';
  docInput.accept = TEXT_FILE_ACCEPT;
  docInput.multiple = true;
  docInput.hidden = true;

  // Everything that changes the next message, rather than sending it, lives
  // behind this one control: thinking, attachments, and what is left of the
  // allowance.
  const menu: ComposerMenu = createComposerMenu({
    reasoningAvailable: () => status.reasoningAvailable,
    reasoning: () => ({ enabled: status.reasoningEnabled, effort: status.reasoningEffort as ReasoningState['effort'] }),
    onReasoningChange: (next) => {
      options.onReasoningChange(next);
      status = options.getStatus();
      menu.sync();
    },
    onPickImages: () => fileInput.click(),
    onPickFiles: () => docInput.click(),
    enabled: () => !busy && status.configured,
    imagesAvailable: () => status.vision,
  });

  const sendBtn = button('ai-chat-send', '');
  const sendIcon = icon(ICONS.send, 16);
  const stopIcon = icon(ICONS.stop, 14);
  stopIcon.style.display = 'none';
  sendBtn.appendChild(sendIcon);
  sendBtn.appendChild(stopIcon);
  sendBtn.title = t('send');
  sendBtn.setAttribute('aria-label', t('send'));

  composerRow.appendChild(menu.element);
  composerRow.appendChild(input);
  composerRow.appendChild(sendBtn);
  composer.appendChild(pendingStrip);
  composer.appendChild(composerRow);
  composer.appendChild(fileInput);
  composer.appendChild(docInput);

  const dropHint = el('div', 'ai-chat-drop', t('dropHint'));

  main.appendChild(bar);
  main.appendChild(scroll);
  main.appendChild(flashLine);
  main.appendChild(composer);
  main.appendChild(dropHint);
  host.appendChild(sidebar);
  host.appendChild(main);

  // --- helpers -------------------------------------------------------------

  function active(): Conversation | null {
    return conversations.find((entry) => entry.id === activeID) ?? null;
  }

  function setFlash(text: string): void {
    flash = text;
    flashLine.textContent = flash;
    flashLine.classList.toggle('visible', !!flash);
  }

  function scrollToEnd(): void {
    scroll.scrollTop = scroll.scrollHeight;
  }

  async function copyText(text: string): Promise<void> {
    try {
      await navigator.clipboard.writeText(text);
      setFlash(t('copied'));
    } catch {
      // Clipboard permission is not guaranteed in every browser, and a failed
      // copy is not worth an error state in the transcript.
    }
  }

  // --- attachments ----------------------------------------------------------

  function thumbnail(src: string, alt: string, onRemove?: () => void): HTMLElement {
    const chip = el('div', 'ai-chat-attachment');
    const thumb = el('img', 'ai-chat-attachment-img');
    thumb.src = src;
    thumb.alt = alt;
    thumb.draggable = false;
    chip.appendChild(thumb);
    if (onRemove) {
      const remove = iconButton('ai-chat-attachment-remove', ICONS.close, t('removeImage'), onRemove, 11);
      chip.appendChild(remove);
    }
    return chip;
  }

  function renderComposerAttachments(): void {
    clear(pendingStrip);
    attachments.forEach((entry, index) => {
      pendingStrip.appendChild(
        thumbnail(entry.preview, '', () => {
          URL.revokeObjectURL(entry.preview);
          attachments.splice(index, 1);
          renderComposerAttachments();
        }),
      );
    });
    menu.sync();
  }

  // One file at a time rather than in parallel: each is decoded and
  // re-encoded through a canvas, and a handful of large photos at once is
  // enough to lock up the page for a noticeable moment.
  async function addFiles(list: FileList | File[] | null): Promise<void> {
    if (busy || !status.configured || !status.vision) return;

    const files = Array.from(list ?? []).filter((file) => file.type.startsWith('image/'));
    if (!files.length) return;

    const room = MAX_IMAGES - attachments.length;
    if (files.length > room) setFlash(t('tooManyImages', { count: MAX_IMAGES }));
    if (room <= 0) return;

    for (const file of files.slice(0, room)) {
      let prepared: PreparedImage;
      try {
        prepared = await prepareImage(file);
      } catch (error) {
        setFlash(error instanceof ImageError && error.kind === 'too-large' ? t('imageTooLarge') : t('imageFailed'));
        continue;
      }
      try {
        const { attachment } = await uploadAttachment({
          mime: prepared.mime,
          data: prepared.data,
          width: prepared.width,
          height: prepared.height,
        });
        attachments.push({ ref: attachment, preview: prepared.previewURL });
        renderComposerAttachments();
      } catch (error) {
        URL.revokeObjectURL(prepared.previewURL);
        setFlash(error instanceof ApiError ? error.message : t('imageFailed'));
      }
    }
  }

  // Files are folded into the composer's text, which is also why the result
  // is editable before it is sent: the user can see exactly what the model
  // will receive.
  async function addTextFiles(list: FileList | File[] | null): Promise<void> {
    if (busy || !status.configured) return;

    for (const file of Array.from(list ?? [])) {
      if (file.size > MAX_FILE_BYTES) {
        setFlash(t('fileTooLarge'));
        continue;
      }

      let text: string;
      try {
        text = await file.text();
      } catch {
        setFlash(t('imageFailed'));
        continue;
      }

      // A binary file read as text comes back full of replacement characters;
      // sending that wastes tokens and tells the model nothing.
      if (looksBinary(text)) {
        setFlash(t('fileUnsupported'));
        continue;
      }

      let body = text;
      if (body.length > MAX_FILE_CHARS) {
        body = `${body.slice(0, MAX_FILE_CHARS)}\n… (truncated)`;
      }

      const fence = '```';
      const block = `${file.name}:\n\n${fence}${fenceLanguage(file.name)}\n${body}\n${fence}\n`;
      const existing = input.value.replace(/\s*$/, '');
      input.value = existing ? `${existing}\n\n${block}` : block;
      setFlash(t('fileAdded', { name: file.name }));
    }

    resizeInput();
    input.focus();
    input.setSelectionRange(input.value.length, input.value.length);
  }

  function carriesFiles(event: DragEvent): boolean {
    if (!status.vision) return false;
    const types = event.dataTransfer?.types;
    return !!types && Array.prototype.indexOf.call(types, 'Files') !== -1;
  }

  let dragging = false;
  function setDragging(state: boolean): void {
    if (dragging === state) return;
    dragging = state;
    host.classList.toggle('dragging', state);
  }

  // --- rendering ------------------------------------------------------------

  function renderSidebar(switched: boolean): void {
    sidebarFoot.hidden = conversations.length === 0;
    clear(sidebarList);

    if (!conversations.length) {
      sidebarList.appendChild(el('p', 'ai-chat-list-empty', t('noHistory')));
      return;
    }

    for (const conversation of conversations) {
      const isActive = conversation.id === activeID;
      const row = el('div', `ai-chat-list-item${isActive ? ' active' : ''}${isActive && switched ? ' switched' : ''}`);

      const open = button('ai-chat-list-open', '');
      open.appendChild(el('span', 'ai-chat-list-title', conversation.title || t('newChat')));
      open.addEventListener('click', () => void openConversation(conversation.id));
      open.addEventListener('dblclick', () => void rename(conversation));

      const remove = iconButton('ai-chat-list-delete', ICONS.trash, t('deleteChat'), () => {
        void removeConversation(conversation);
      }, 13);

      row.appendChild(open);
      row.appendChild(remove);
      sidebarList.appendChild(row);
    }
  }

  function renderSetup(): HTMLElement {
    const card = el('div', 'ai-chat-setup');
    card.appendChild(el('h3', 'ai-chat-setup-title', t('setupTitle')));
    card.appendChild(el('p', 'ai-chat-setup-body', status.canAdminister ? t('setupBody') : t('setupBodyUser')));
    if (status.canAdminister) {
      card.appendChild(button('ai-chat-setup-action', t('setupAction'), () => options.onOpenSetup()));
    }
    return card;
  }

  function renderEmpty(): HTMLElement {
    const empty = el('div', 'ai-chat-empty');
    empty.appendChild(el('h3', 'ai-chat-empty-title', t('emptyTitle')));
    empty.appendChild(el('p', 'ai-chat-empty-body', t('emptyBody')));

    const suggestions = el('div', 'ai-chat-suggestions');
    for (const key of shownSuggestions) {
      suggestions.appendChild(button('ai-chat-suggestion', t(key), () => {
        input.value = t(key);
        void submit();
      }));
    }
    empty.appendChild(suggestions);
    return empty;
  }

  function renderAttachmentStrip(list: AttachmentRef[]): HTMLElement {
    const strip = el('div', 'ai-chat-attachments');
    for (const attachment of list) {
      strip.appendChild(thumbnail(attachmentURL(attachment.id), ''));
    }
    return strip;
  }

  function renderUserMessage(message: Message): HTMLElement {
    const row = el('div', 'ai-msg ai-msg-user');
    if (message.id === justSentID) row.classList.add('ai-msg-sent');

    const images = message.attachments ?? [];
    if (images.length) row.appendChild(renderAttachmentStrip(images));

    if (editingID === message.id) {
      const editor = el('div', 'ai-msg-editor');
      const area = el('textarea', 'ai-chat-input ai-msg-edit-input');
      area.maxLength = MAX_MESSAGE_CHARS;
      area.value = message.content;
      area.rows = Math.min(8, Math.max(2, message.content.split('\n').length + 1));

      const actions = el('div', 'ai-msg-editor-actions');
      actions.appendChild(button('ai-chat-mini-btn', t('cancel'), () => {
        editingID = '';
        render();
      }));
      actions.appendChild(button('ai-chat-mini-btn primary', t('saveAndResend'), () => {
        const text = area.value.trim();
        editingID = '';
        // Editing rewrites history from this point: everything after it was
        // an answer to a question that no longer exists.
        void runTurn({
          content: text,
          truncateFrom: message.id,
          attachmentIDs: images.map((image) => image.id),
        });
      }));

      editor.appendChild(area);
      editor.appendChild(actions);
      row.appendChild(editor);
      requestAnimationFrame(() => area.focus());
      return row;
    }

    if (message.content || !images.length) {
      row.appendChild(el('div', 'ai-bubble', message.content));
    }

    const actions = el('div', 'ai-msg-actions');
    actions.appendChild(button('ai-chat-mini-btn', t('edit'), () => {
      editingID = message.id;
      render();
    }));
    actions.appendChild(button('ai-chat-mini-btn', t('copy'), () => void copyText(message.content)));
    row.appendChild(actions);
    return row;
  }

  function renderThinking(text: string, live: boolean): HTMLDetailsElement {
    const box = el('details', 'ai-thinking');
    if (live) box.open = true;
    box.appendChild(el('summary', 'ai-thinking-head', t(live ? 'reasoningLive' : 'reasoning')));
    const body = el('div', 'ai-thinking-body', text);
    box.appendChild(body);
    if (live) requestAnimationFrame(() => { body.scrollTop = body.scrollHeight; });
    return box;
  }

  function renderStats(stats: MessageStats): HTMLElement {
    const seconds = (ms: number) => (ms >= 10000 ? String(Math.round(ms / 1000)) : (Math.round(ms / 100) / 10).toFixed(1));
    const parts = [`${seconds(stats.ms)}s`];
    parts.push(t(stats.streamed ? 'statsStreamed' : 'statsOneShot'));
    if (stats.first_token_ms !== undefined) {
      parts.push(t('statsFirstToken', { seconds: seconds(stats.first_token_ms) }));
    }
    if (stats.output_tokens !== undefined) {
      parts.push(stats.input_tokens === undefined
        ? t('statsOutputOnly', { output: stats.output_tokens })
        : t('statsTokens', { input: stats.input_tokens, output: stats.output_tokens }));
    }
    if (stats.tps !== undefined) parts.push(t('statsSpeed', { tps: stats.tps }));
    return el('div', 'ai-msg-stats', parts.join(' · '));
  }

  function renderAssistantMessage(message: Message): HTMLElement {
    const row = el('div', 'ai-msg ai-msg-assistant');

    if (message.error) {
      const failure = el('div', 'ai-chat-error');
      failure.appendChild(el('span', 'ai-chat-error-text', message.error));
      failure.appendChild(button('ai-chat-mini-btn', t('retry'), () => {
        void runTurn({ truncateFrom: message.id });
      }));
      row.appendChild(failure);
      return row;
    }

    if (message.reasoning) row.appendChild(renderThinking(message.reasoning, false));

    const answer = el('div', 'ai-answer');
    renderInto(answer, message.content);
    row.appendChild(answer);

    const actions = el('div', 'ai-msg-actions');
    actions.appendChild(button('ai-chat-mini-btn', t('regenerate'), () => {
      void runTurn({ truncateFrom: message.id });
    }));
    actions.appendChild(button('ai-chat-mini-btn', t('copy'), () => void copyText(message.content)));
    row.appendChild(actions);

    if (message.stats) row.appendChild(renderStats(message.stats));
    return row;
  }

  function renderPending(): HTMLElement {
    const row = el('div', 'ai-msg ai-msg-assistant');
    pendingNodes = { reasoning: null, answer: null };

    if (pending?.reasoning) {
      const box = renderThinking(pending.reasoning, true);
      pendingNodes.reasoning = box.querySelector('.ai-thinking-body');
      row.appendChild(box);
    }
    if (pending?.answer) {
      const answer = el('div', 'ai-answer ai-answer-streaming');
      renderInto(answer, pending.answer);
      pendingNodes.answer = answer;
      row.appendChild(answer);
      return row;
    }

    const spinner = el('div', 'ai-chat-pending');
    spinner.appendChild(el('span', 'ai-chat-spinner'));
    spinner.appendChild(el('span', null, t('thinking')));
    row.appendChild(spinner);
    return row;
  }

  // The streaming fast path. A full render on every delta would rebuild the
  // whole transcript dozens of times a second; this patches the two nodes
  // that changed, and falls back to a render only when the shape of the
  // pending message changes (reasoning appearing, the first token arriving).
  function updatePending(): boolean {
    if (!pendingNodes || !pending) return false;
    if (!!pending.reasoning !== !!pendingNodes.reasoning) return false;
    if (!!pending.answer !== !!pendingNodes.answer) return false;

    const box = pendingNodes.reasoning;
    if (box) {
      const atEnd = box.scrollHeight - box.scrollTop - box.clientHeight < 24;
      box.textContent = pending.reasoning;
      if (atEnd) box.scrollTop = box.scrollHeight;
    }
    if (pendingNodes.answer) renderInto(pendingNodes.answer, pending.answer);
    return true;
  }

  function render(): void {
    if (destroyed) return;
    pendingNodes = null;

    const conversation = active();
    const switched = activeID !== lastRenderedID;
    lastRenderedID = activeID;

    host.classList.toggle('history-open', historyOpen);
    barTitle.textContent = conversation?.title || t('brand');
    renderSidebar(switched);

    if (switched) {
      // A short rise says "a different conversation" instead of leaving the
      // transcript to flicker into something else within one frame.
      scroll.classList.remove('ai-chat-switching');
      void scroll.offsetWidth;
      scroll.classList.add('ai-chat-switching');
    }

    clear(scroll);
    if (!status.configured) scroll.appendChild(renderSetup());

    if (messages.length) {
      for (const message of messages) {
        scroll.appendChild(
          message.role === 'user' ? renderUserMessage(message) : renderAssistantMessage(message),
        );
      }
    } else if (status.configured) {
      scroll.appendChild(renderEmpty());
    }

    if (busy) scroll.appendChild(renderPending());

    const empty = messages.length === 0 && !busy;
    host.classList.toggle('is-empty', empty);
    input.placeholder = t(messages.length ? 'placeholder' : 'placeholderFirst');
    input.disabled = busy || !status.configured;
    syncSendButton();
    renderComposerAttachments();
  }

  function syncSendButton(): void {
    const stoppable = busy && controller !== null;
    sendBtn.classList.toggle('stop', stoppable);
    sendBtn.disabled = busy ? !stoppable : !status.configured;
    sendIcon.style.display = stoppable ? 'none' : '';
    stopIcon.style.display = stoppable ? '' : 'none';
    const label = t(stoppable ? 'stop' : 'send');
    sendBtn.title = label;
    sendBtn.setAttribute('aria-label', label);
  }

  // --- turns -----------------------------------------------------------------

  interface TurnOptions {
    content?: string;
    attachmentIDs?: string[];
    truncateFrom?: string;
  }

  async function runTurn(turn: TurnOptions): Promise<void> {
    if (busy || !status.configured) return;

    // Optimistic local rewind, so the transcript reacts before the server
    // answers. The server does the same thing to the rows.
    if (turn.truncateFrom) {
      const cut = messages.findIndex((message) => message.id === turn.truncateFrom);
      if (cut >= 0) messages = messages.slice(0, cut);
    }
    if (turn.content) {
      messages = messages.concat([{
        id: `local-${Date.now()}`,
        seq: messages.length + 1,
        role: 'user',
        content: turn.content,
        attachments: attachments.map((entry) => entry.ref),
        created_at: Date.now(),
      }]);
      justSentID = messages[messages.length - 1]!.id;
    }

    busy = true;
    pending = { answer: '', reasoning: '' };
    setFlash('');
    render();
    scrollToEnd();
    justSentID = '';

    controller = new AbortController();
    syncSendButton();

    // Only auto-scroll when the reader is already at the bottom: yanking the
    // view back while someone is reading an earlier part of the answer is
    // worse than letting it run off screen.
    const stream = (apply: () => void) => {
      const atEnd = scroll.scrollHeight - scroll.scrollTop - scroll.clientHeight < 40;
      apply();
      if (!updatePending()) render();
      if (atEnd) scrollToEnd();
    };

    let failed = false;

    try {
      await sendTurn(
        {
          ...(activeID ? { conversation_id: activeID } : {}),
          model_id: status.modelID,
          ...(turn.content ? { content: turn.content } : {}),
          ...(turn.attachmentIDs?.length ? { attachment_ids: turn.attachmentIDs } : {}),
          ...(turn.truncateFrom ? { truncate_from_message_id: turn.truncateFrom } : {}),
          reasoning: {
            enabled: status.reasoningAvailable && status.reasoningEnabled,
            effort: status.reasoningEffort,
          },
        },
        {
          onStart: (payload) => {
            if (!activeID) {
              activeID = payload.conversation_id;
              lastRenderedID = activeID;
            }
            upsertConversationStub(payload.conversation_id, payload.title);
          },
          onDelta: (text) => stream(() => { pending!.answer += text; }),
          onReasoning: (text) => stream(() => { pending!.reasoning += text; }),
          onDone: (payload) => {
            if (payload.stream_fallback) {
              setFlash(t('streamFallback', { reason: payload.stream_fallback }));
            } else if (payload.stopped) {
              setFlash(t('stopped'));
            }
          },
          onError: (payload) => {
            failed = true;
            setFlash(payload.message);
          },
        },
        controller.signal,
      );
    } catch (error) {
      if (!controller.signal.aborted) {
        failed = true;
        setFlash(error instanceof ApiError ? error.message : String(error));
      }
    } finally {
      busy = false;
      pending = null;
      controller = null;
    }

    // The server owns the transcript, so the authoritative version is read
    // back rather than reconstructed from what streamed. It also fills in the
    // message ids, the stats and — when a turn failed — the error row.
    await reloadActive();
    render();
    scrollToEnd();
    if (!failed) setFlash(flash);
    void refreshList();
  }

  async function submit(): Promise<void> {
    if (busy || !status.configured) return;

    const text = input.value.trim();
    const ids = attachments.map((entry) => entry.ref.id);
    if (!text && !ids.length) return;

    input.value = '';
    resizeInput();

    const previews = attachments.map((entry) => entry.preview);
    await runTurn({ content: text, attachmentIDs: ids });

    for (const url of previews) URL.revokeObjectURL(url);
    attachments = [];
    renderComposerAttachments();
  }

  // --- conversations ---------------------------------------------------------

  function upsertConversationStub(id: string, title: string): void {
    const existing = conversations.find((entry) => entry.id === id);
    if (existing) {
      if (title) existing.title = title;
      return;
    }
    conversations = [{
      id,
      title,
      model_id: status.modelID,
      pinned: false,
      message_count: 0,
      created_at: Date.now(),
      updated_at: Date.now(),
    }, ...conversations];
    renderSidebar(true);
  }

  async function refreshList(): Promise<void> {
    try {
      const { conversations: list } = await listConversations();
      conversations = list;
      if (!destroyed) renderSidebar(false);
    } catch {
      // The rail is a convenience; a failed refresh leaves the last copy.
    }
  }

  async function reloadActive(): Promise<void> {
    if (!activeID) {
      messages = [];
      return;
    }
    try {
      const { conversation, messages: list } = await getConversation(activeID);
      messages = list;
      options.onConversationChange?.(conversation);
    } catch (error) {
      if (error instanceof ApiError && error.status === 404) {
        activeID = '';
        messages = [];
      }
    }
  }

  async function openConversation(id: string): Promise<void> {
    if (busy) return;
    activeID = id;
    editingID = '';
    historyOpen = false;
    messages = [];
    render();
    await reloadActive();
    render();
    scrollToEnd();
  }

  function startNewConversation(): void {
    if (busy) return;
    activeID = '';
    messages = [];
    editingID = '';
    historyOpen = false;
    clearAttachments();
    options.onConversationChange?.(null);
    render();
    input.focus();
  }

  async function rename(conversation: Conversation): Promise<void> {
    const next = window.prompt(t('renamePrompt'), conversation.title);
    if (next === null) return;
    try {
      const { conversation: updated } = await renameConversation(conversation.id, next.trim());
      conversation.title = updated.title;
      render();
    } catch (error) {
      setFlash(error instanceof ApiError ? error.message : String(error));
    }
  }

  async function removeConversation(conversation: Conversation): Promise<void> {
    if (!window.confirm(t('confirmDelete'))) return;
    try {
      await deleteConversation(conversation.id);
    } catch (error) {
      setFlash(error instanceof ApiError ? error.message : String(error));
      return;
    }
    conversations = conversations.filter((entry) => entry.id !== conversation.id);
    if (activeID === conversation.id) {
      activeID = '';
      messages = [];
      options.onConversationChange?.(null);
    }
    render();
  }

  async function clearEverything(): Promise<void> {
    if (!conversations.length || !window.confirm(t('confirmClearAll'))) return;
    try {
      await deleteAllConversations();
    } catch (error) {
      setFlash(error instanceof ApiError ? error.message : String(error));
      return;
    }
    conversations = [];
    activeID = '';
    messages = [];
    options.onConversationChange?.(null);
    render();
  }

  function clearAttachments(): void {
    for (const entry of attachments) URL.revokeObjectURL(entry.preview);
    attachments = [];
  }

  // --- composer ---------------------------------------------------------------

  function resizeInput(): void {
    input.style.height = 'auto';
    input.style.height = `${Math.min(200, input.scrollHeight)}px`;
  }

  composer.addEventListener('submit', (event) => {
    event.preventDefault();
    void submit();
  });

  sendBtn.addEventListener('click', (event) => {
    event.preventDefault();
    if (busy) {
      // Aborting the fetch closes the connection, which cancels the server's
      // request context, which cancels the provider call. One line, and the
      // upstream stops billing.
      controller?.abort();
      return;
    }
    void submit();
  });

  input.addEventListener('input', resizeInput);

  fileInput.addEventListener('change', () => {
    const files = Array.from(fileInput.files ?? []);
    fileInput.value = '';
    void addFiles(files);
  });

  docInput.addEventListener('change', () => {
    const files = Array.from(docInput.files ?? []);
    docInput.value = '';
    void addTextFiles(files);
  });

  input.addEventListener('paste', (event) => {
    const files = Array.from(event.clipboardData?.files ?? []);
    if (!files.length) return;
    event.preventDefault();
    void addFiles(files);
  });

  for (const type of ['dragenter', 'dragover'] as const) {
    main.addEventListener(type, (event) => {
      if (!carriesFiles(event)) return;
      event.preventDefault();
      if (event.dataTransfer) event.dataTransfer.dropEffect = 'copy';
      setDragging(true);
    });
  }

  main.addEventListener('dragleave', (event) => {
    if (event.target === main || !main.contains(event.relatedTarget as Node | null)) setDragging(false);
  });

  main.addEventListener('drop', (event) => {
    if (!carriesFiles(event)) return;
    event.preventDefault();
    setDragging(false);
    void addFiles(event.dataTransfer?.files ?? null);
  });

  input.addEventListener('keydown', (event) => {
    if (event.key !== 'Enter' || event.shiftKey || event.isComposing) return;
    event.preventDefault();
    void submit();
  });

  // --- boot ---------------------------------------------------------------------

  const ready = (async () => {
    await refreshList();
    render();
    resizeInput();
  })();

  return {
    ready,
    refreshStatus() {
      status = options.getStatus();
      render();
    },
    newConversation: startNewConversation,
    toggleHistory() {
      historyOpen = !historyOpen;
      render();
    },
    focus() {
      input.focus({ preventScroll: true });
    },
    destroy() {
      destroyed = true;
      controller?.abort();
      clearAttachments();
    },
  };
}

function pickSuggestions(count: number): Array<(typeof SUGGESTION_KEYS)[number]> {
  const pool = [...SUGGESTION_KEYS];
  const picked: Array<(typeof SUGGESTION_KEYS)[number]> = [];
  while (picked.length < count && pool.length) {
    picked.push(pool.splice(Math.floor(Math.random() * pool.length), 1)[0]!);
  }
  return picked;
}

// A file the browser decoded as UTF-8 but that was never text shows up as a
// run of replacement characters. A handful is a mangled accent; a fifth of
// the file is a binary.
function looksBinary(text: string): boolean {
  if (!text) return false;
  const sample = text.slice(0, 4096);
  let suspicious = 0;
  for (const char of sample) {
    const code = char.codePointAt(0)!;
    if (code === 0xfffd || code === 0 || (code < 9 && code !== 0)) suspicious++;
  }
  return suspicious / sample.length > 0.05;
}
