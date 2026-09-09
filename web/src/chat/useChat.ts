// The chat surface's state: conversations, the open transcript, the turn
// being streamed, and what is attached to the next message.
//
// Ported from the standalone build with its behaviour intact — the same
// optimistic rewind before a turn, the same authoritative reload after one,
// the same "abort the fetch is the stop button" cancellation.
//
// What changed is that the render is no longer part of it. The hand-written
// version rebuilt the whole transcript on every change with a hand-rolled
// fast path for the streaming message; here the state is refs and Vue only
// touches the message that moved. The fast path was the thing most likely to
// go subtly wrong, and it is gone rather than reimplemented.

import { computed, ref } from 'vue';
import {
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
} from '@/api/chat';
import { ApiError } from '@/api/client';
import { t, type StringKey } from '@/composables/useI18n';
import { currentPreferences, currentUser } from '@/stores/session';
import { ImageError, prepareImage, type PreparedImage } from './image';
import { currentModel, reasoning } from './useModels';

export const MAX_MESSAGE_CHARS = 32000;
export const MAX_IMAGES = 6;
const SUGGESTIONS_SHOWN = 3;

// A text file becomes part of the message rather than an attachment: both
// provider protocols take images and text, and nothing else, so a document
// that a model can read as characters belongs in the prompt. Anything else —
// a PDF, a spreadsheet — would have to be converted server-side, which is a
// feature and not a file picker.
const MAX_FILE_BYTES = 256 * 1024;
const MAX_FILE_CHARS = 24000;

export const TEXT_FILE_ACCEPT = [
  '.txt', '.md', '.markdown', '.csv', '.tsv', '.json', '.jsonl', '.yaml', '.yml',
  '.toml', '.ini', '.cfg', '.conf', '.env', '.log', '.xml', '.html', '.css',
  '.js', '.jsx', '.ts', '.tsx', '.go', '.rs', '.py', '.rb', '.java', '.kt',
  '.c', '.h', '.cc', '.cpp', '.hpp', '.cs', '.php', '.swift', '.sh', '.bash',
  '.zsh', '.ps1', '.sql', '.graphql', '.proto', '.dockerfile', '.gitignore',
  'text/*',
].join(',');

// Far more suggestions than fit on screen, so each visit offers a different
// three. A fixed trio is read once and then becomes furniture; a rotating one
// keeps suggesting things the user did not know to ask for.
const SUGGESTION_KEYS = [
  'suggestionExplain', 'suggestionBrainstorm', 'suggestionWrite', 'suggestionSummarize',
  'suggestionDebug', 'suggestionPlan', 'suggestionCompare', 'suggestionRewrite',
  'suggestionLearn', 'suggestionDecide', 'suggestionDraft', 'suggestionCritique',
] as const;

/** A message being streamed, before it becomes a row. */
export interface Pending {
  answer: string;
  reasoning: string;
  /** When the turn was sent, so the timer under it counts from the send. */
  startedAt: number;
}

export interface ComposerAttachment {
  ref: AttachmentRef;
  preview: string;
}

// --- state ---------------------------------------------------------------------

export const conversations = ref<Conversation[]>([]);
export const activeID = ref('');
export const messages = ref<Message[]>([]);
export const busy = ref(false);
export const editingID = ref('');
export const justSentID = ref('');
export const historyOpen = ref(false);
export const flash = ref('');
export const pending = ref<Pending | null>(null);
export const attachments = ref<ComposerAttachment[]>([]);
export const draft = ref('');
/** True while files are being dragged over the transcript. */
export const dragging = ref(false);
export const suggestions = ref<Array<(typeof SUGGESTION_KEYS)[number]>>(pickSuggestions(SUGGESTIONS_SHOWN));

let controller: AbortController | null = null;

/**
 * Deltas arrive faster than the screen redraws, so they are applied per frame.
 *
 * A model streams tokens tens of times a second, and every one of them used to
 * replace the pending message outright — which re-parses the whole answer into
 * DOM (chat/markdown.ts rebuilds the subtree rather than patching it) and reads
 * the scroll geometry back. That is work proportional to the answer so far,
 * done once per token, so a long answer grew slower the longer it got.
 * Buffering into one animation frame bounds it at the refresh rate, which is
 * all a reader can see in any case.
 */
let frame = 0;
let bufferedAnswer = '';
let bufferedReasoning = '';

/** Exported so a test can prove the per-frame bound rather than assume it. */
export function flushDeltas() {
  frame = 0;
  const answer = bufferedAnswer;
  const reasoning = bufferedReasoning;
  bufferedAnswer = '';
  bufferedReasoning = '';
  if (!answer && !reasoning) return;
  if (pending.value) {
    pending.value = {
      ...pending.value,
      answer: pending.value.answer + answer,
      reasoning: pending.value.reasoning + reasoning,
    };
  }
  scrollToEnd();
}

/** Buffers one delta. Exported alongside flushDeltas, and for the same reason. */
export function bufferDelta(answer: string, reasoning = '') {
  bufferedAnswer += answer;
  bufferedReasoning += reasoning;
  if (!frame) frame = requestAnimationFrame(flushDeltas);
}

/**
 * Applies whatever is still buffered and stops the pending frame.
 *
 * Called when a turn ends, so the next one cannot inherit the tail of this
 * one — and because a background tab never runs an animation frame, which
 * would otherwise leave the buffer holding a whole answer.
 *
 * Exported alongside the other two so the cross-turn drain is covered.
 */
export function settleDeltas() {
  if (frame) cancelAnimationFrame(frame);
  flushDeltas();
}

/**
 * Where the transcript should scroll to, and whether it may.
 *
 * Set by the store, read by the surface: yanking the view back while somebody
 * is reading an earlier part of an answer is worse than letting it run off
 * screen, so the component decides whether it was at the end and the store
 * only says that something arrived.
 */
export const scrollTick = ref(0);
export const switchTick = ref(0);

export const active = computed(() => conversations.value.find((entry) => entry.id === activeID.value) ?? null);
export const isEmpty = computed(() => messages.value.length === 0 && !busy.value);
export const stoppable = computed(() => busy.value && controller !== null);

/**
 * Read live rather than captured: the settings panel can flip the preference
 * while the transcript is on screen, and the group's permission is the
 * ceiling over it — a stored `true` from before a group lost the capability
 * must not keep showing the line.
 */
export const statsWanted = computed(
  () => currentUser.value?.allow_stats !== false && currentPreferences.value['show_stats'] === true,
);
export const canDelete = computed(() => currentUser.value?.allow_delete_conversations !== false);

/** What the composer and the transcript need to know about the chosen model. */
export const status = computed(() => {
  const model = currentModel.value;
  // A model that cannot reason must not carry a stale toggle: the gateway
  // would drop it, and the chip would be claiming something untrue.
  const canReason = !!model?.supports_reasoning;
  return {
    configured: model !== null,
    vision: !!model?.supports_images,
    reasoningAvailable: canReason,
    reasoningEnabled: canReason && reasoning.value.enabled,
    reasoningEffort: reasoning.value.effort,
    modelID: model?.id ?? '',
    modelUnstable: !!model?.unstable,
  };
});

function setFlash(text: string): void {
  flash.value = text;
}

function scrollToEnd(): void {
  scrollTick.value += 1;
}

// --- attachments ----------------------------------------------------------------

/**
 * One file at a time rather than in parallel: each is decoded and re-encoded
 * through a canvas, and a handful of large photos at once is enough to lock
 * up the page for a noticeable moment.
 */
export async function addImages(list: FileList | File[] | null): Promise<void> {
  if (busy.value || !status.value.configured || !status.value.vision) return;

  const files = Array.from(list ?? []).filter((file) => file.type.startsWith('image/'));
  if (!files.length) return;

  const room = MAX_IMAGES - attachments.value.length;
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
      attachments.value = [...attachments.value, { ref: attachment, preview: prepared.previewURL }];
    } catch (error) {
      URL.revokeObjectURL(prepared.previewURL);
      setFlash(error instanceof ApiError ? error.message : t('imageFailed'));
    }
  }
}

export function removeAttachment(index: number): void {
  const entry = attachments.value[index];
  if (!entry) return;
  URL.revokeObjectURL(entry.preview);
  attachments.value = attachments.value.filter((_, at) => at !== index);
}

function clearAttachments(): void {
  for (const entry of attachments.value) URL.revokeObjectURL(entry.preview);
  attachments.value = [];
}

/**
 * Files are folded into the composer's text, which is also why the result is
 * editable before it is sent: the user can see exactly what the model will
 * receive.
 */
export async function addTextFiles(list: FileList | File[] | null): Promise<void> {
  if (busy.value || !status.value.configured) return;

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
    if (body.length > MAX_FILE_CHARS) body = `${body.slice(0, MAX_FILE_CHARS)}\n… (truncated)`;

    const fence = '```';
    const block = `${file.name}:\n\n${fence}${fenceLanguage(file.name)}\n${body}\n${fence}\n`;
    const existing = draft.value.replace(/\s*$/, '');
    draft.value = existing ? `${existing}\n\n${block}` : block;
    setFlash(t('fileAdded', { name: file.name }));
  }
}

// --- turns ----------------------------------------------------------------------

interface TurnOptions {
  content?: string;
  attachmentIDs?: string[];
  truncateFrom?: string;
}

export async function runTurn(turn: TurnOptions): Promise<void> {
  if (busy.value || !status.value.configured) return;

  // Optimistic local rewind, so the transcript reacts before the server
  // answers. The server does the same thing to the rows.
  if (turn.truncateFrom) {
    const cut = messages.value.findIndex((message) => message.id === turn.truncateFrom);
    if (cut >= 0) messages.value = messages.value.slice(0, cut);
  }
  if (turn.content) {
    messages.value = messages.value.concat([{
      id: `local-${Date.now()}`,
      seq: messages.value.length + 1,
      role: 'user',
      content: turn.content,
      attachments: attachments.value.map((entry) => entry.ref),
      created_at: Date.now(),
    }]);
    justSentID.value = messages.value[messages.value.length - 1]!.id;
  }

  busy.value = true;
  editingID.value = '';
  pending.value = { answer: '', reasoning: '', startedAt: Date.now() };
  setFlash('');
  scrollToEnd();
  justSentID.value = '';

  controller = new AbortController();
  let failed = false;

  try {
    await sendTurn(
      {
        ...(activeID.value ? { conversation_id: activeID.value } : {}),
        model_id: status.value.modelID,
        ...(turn.content ? { content: turn.content } : {}),
        ...(turn.attachmentIDs?.length ? { attachment_ids: turn.attachmentIDs } : {}),
        ...(turn.truncateFrom ? { truncate_from_message_id: turn.truncateFrom } : {}),
        reasoning: {
          enabled: status.value.reasoningAvailable && status.value.reasoningEnabled,
          effort: status.value.reasoningEffort,
        },
      },
      {
        onStart: (payload) => {
          if (!activeID.value) activeID.value = payload.conversation_id;
          upsertConversationStub(payload.conversation_id, payload.title);
        },
        onDelta: (text) => bufferDelta(text),
        onReasoning: (text) => bufferDelta('', text),
        onDone: (payload) => {
          if (payload.stream_fallback) setFlash(t('streamFallback', { reason: payload.stream_fallback }));
          else if (payload.stopped) setFlash(t('stopped'));
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
    settleDeltas();
    busy.value = false;
    pending.value = null;
    controller = null;
  }

  // The server owns the transcript, so the authoritative version is read back
  // rather than reconstructed from what streamed. It also fills in the message
  // ids, the stats and — when a turn failed — the error row.
  await reloadActive();
  scrollToEnd();
  if (!failed) setFlash(flash.value);
  void refreshList();
}

export async function submit(): Promise<void> {
  if (busy.value || !status.value.configured) return;

  const text = draft.value.trim();
  const ids = attachments.value.map((entry) => entry.ref.id);
  if (!text && !ids.length) return;

  draft.value = '';
  const previews = attachments.value.map((entry) => entry.preview);
  await runTurn({ content: text, attachmentIDs: ids });

  for (const url of previews) URL.revokeObjectURL(url);
  attachments.value = [];
}

/**
 * Aborting the fetch closes the connection, which cancels the server's
 * request context, which cancels the provider call. One line, and the
 * upstream stops billing.
 */
export function stop(): void {
  controller?.abort();
}

// --- conversations ---------------------------------------------------------------

function upsertConversationStub(id: string, title: string): void {
  const existing = conversations.value.find((entry) => entry.id === id);
  if (existing) {
    if (title) existing.title = title;
    return;
  }
  conversations.value = [{
    id,
    title,
    model_id: status.value.modelID,
    pinned: false,
    message_count: 0,
    created_at: Date.now(),
    updated_at: Date.now(),
  }, ...conversations.value];
}

export async function refreshList(): Promise<void> {
  try {
    const { conversations: list } = await listConversations();
    conversations.value = list;
  } catch {
    // The rail is a convenience; a failed refresh leaves the last copy.
  }
}

async function reloadActive(): Promise<void> {
  if (!activeID.value) {
    messages.value = [];
    return;
  }
  try {
    const { messages: list } = await getConversation(activeID.value);
    messages.value = list;
  } catch (error) {
    if (error instanceof ApiError && error.status === 404) {
      activeID.value = '';
      messages.value = [];
    }
  }
}

export async function openConversation(id: string): Promise<void> {
  if (busy.value) return;
  activeID.value = id;
  editingID.value = '';
  historyOpen.value = false;
  messages.value = [];
  // A short rise says "a different conversation" instead of leaving the
  // transcript to flicker into something else within one frame.
  switchTick.value += 1;
  await reloadActive();
  scrollToEnd();
}

export function startNewConversation(): void {
  if (busy.value) return;
  activeID.value = '';
  messages.value = [];
  editingID.value = '';
  historyOpen.value = false;
  clearAttachments();
  switchTick.value += 1;
}

export async function rename(conversation: Conversation): Promise<void> {
  const next = window.prompt(t('renamePrompt'), conversation.title);
  if (next === null) return;
  try {
    const { conversation: updated } = await renameConversation(conversation.id, next.trim());
    conversation.title = updated.title;
  } catch (error) {
    setFlash(error instanceof ApiError ? error.message : String(error));
  }
}

export async function removeConversation(conversation: Conversation): Promise<void> {
  try {
    await deleteConversation(conversation.id);
  } catch (error) {
    setFlash(error instanceof ApiError ? error.message : String(error));
    return;
  }
  conversations.value = conversations.value.filter((entry) => entry.id !== conversation.id);
  if (activeID.value === conversation.id) {
    activeID.value = '';
    messages.value = [];
  }
}

export async function clearEverything(): Promise<void> {
  if (!conversations.value.length) return;
  try {
    await deleteAllConversations();
  } catch (error) {
    setFlash(error instanceof ApiError ? error.message : String(error));
    return;
  }
  conversations.value = [];
  activeID.value = '';
  messages.value = [];
}

/**
 * Leaving the chat mid-generation aborts the turn; the server still saves
 * whatever streamed before that.
 */
export function resetChat(): void {
  controller?.abort();
  controller = null;
  clearAttachments();
  conversations.value = [];
  messages.value = [];
  activeID.value = '';
  editingID.value = '';
  draft.value = '';
  flash.value = '';
  pending.value = null;
  busy.value = false;
  suggestions.value = pickSuggestions(SUGGESTIONS_SHOWN);
}

export { setFlash };

// --- odds and ends ----------------------------------------------------------------

function pickSuggestions(count: number): Array<(typeof SUGGESTION_KEYS)[number]> {
  const pool = [...SUGGESTION_KEYS];
  const picked: Array<(typeof SUGGESTION_KEYS)[number]> = [];
  while (picked.length < count && pool.length) {
    picked.push(pool.splice(Math.floor(Math.random() * pool.length), 1)[0]!);
  }
  return picked;
}

/** The fence language, so a code file arrives highlighted rather than as prose. */
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

/**
 * A file the browser decoded as UTF-8 but that was never text shows up as a
 * run of replacement characters. A handful is a mangled accent; a fifth of
 * the file is a binary.
 */
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

export type { StringKey };
