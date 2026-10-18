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

import { computed, ref, shallowRef } from 'vue';
import {
  archiveConversation as archiveConversationApi,
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
import { apiErrorText, storedErrorText } from '@/lib/api-error';
import { pendingProjectID } from '@/stores/workspace';
import { currentPreferences, currentUser, onSignOut } from '@/stores/session';
import { ImageError, prepareImage, type PreparedImage } from './image';
import { currentModel, reasoning } from './useModels';
import { markFlying, resetFlightState } from './useSendAnimation';

export const MAX_MESSAGE_CHARS = 32000;
export const MAX_IMAGES = 6;
/**
 * How many conversations the rail holds — the server's own default, asked for
 * by name so the rail can tell when it is full and its search, which only
 * filters what is held, may be missing older ones.
 */
export const RAIL_LIMIT = 60;
const SUGGESTIONS_SHOWN = 3;

// A text file becomes part of the message rather than an attachment: both
// provider protocols take images and text, and nothing else, so a document
// that a model can read as characters belongs in the prompt. Anything else —
// a PDF, a spreadsheet — would have to be converted server-side, which is a
// feature and not a file picker.
const MAX_FILE_BYTES = 256 * 1024;
const MAX_FILE_CHARS = 24000;
const TRUNCATION_MARK = '\n… (truncated)';

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
export const recentReasoningID = ref('');
export const historyOpen = ref(false);
export const flash = ref('');
export const pending = ref<Pending | null>(null);
/**
 * The conversation the streaming turn belongs to, so the rail can mark the
 * row that is still writing while the reader is somewhere else.
 */
export const pendingID = ref('');
export const attachments = ref<ComposerAttachment[]>([]);
export const draft = ref('');
/** True while files are being dragged over the transcript. */
export const dragging = ref(false);
export const suggestions = ref<Array<(typeof SUGGESTION_KEYS)[number]>>(pickSuggestions(SUGGESTIONS_SHOWN));

// A ref, not a plain variable: stoppable is computed from it, and a plain
// variable would leave that computed stale when a turn hands over to its
// post-turn reload.
const controller = shallowRef<AbortController | null>(null);
// Bumped by resetChat. Whatever an await brings back after a sign-out was
// asked for by the account that signed out, so it is dropped rather than
// written into the next account's composer or rail.
let generation = 0;

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

/**
 * Which view the streamed block belongs to.
 *
 * Switching conversations does not wait for a turn any more, so the pending
 * block has to know whether the screen under it is still the one the turn was
 * sent from. The id alone cannot answer that: a turn is unnamed until the
 * server names it, and "new chat" is a different screen carrying the same
 * empty id. `switchTick` counts explicit switches, so comparing tokens
 * distinguishes them.
 */
const pendingView = ref(-1);

export const active = computed(() => conversations.value.find((entry) => entry.id === activeID.value) ?? null);
export const railFull = computed(() => conversations.value.length >= RAIL_LIMIT);
export const isEmpty = computed(() => messages.value.length === 0 && !busy.value);
export const stoppable = computed(() => busy.value && controller.value !== null);
/** The streamed block is rendered by the conversation it is being written into. */
export const showPending = computed(() => busy.value && pending.value !== null && pendingView.value === switchTick.value);

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
export const canArchive = computed(() => currentUser.value?.allow_archive_conversations !== false);

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

/** Whether the screen still shows the conversation the running turn is in. */
function watching(): boolean {
  return pendingView.value === switchTick.value;
}

function setFlash(text: string): void {
  flash.value = text;
}

/**
 * What a reader is shown for a request that failed. An ApiError carries text the
 * server wrote for a reader; anything else is a network or runtime failure
 * (`TypeError: Failed to fetch`), whose English is for a developer, so it is
 * logged and the translated generic sentence is shown instead.
 */
export function failureText(error: unknown): string {
  if (error instanceof ApiError) return apiErrorText(error);
  console.warn('chat request failed', error);
  return t('failed');
}

function scrollToEnd(): void {
  scrollTick.value += 1;
}

// --- attachments ----------------------------------------------------------------

// Uploads that have started and not yet landed. They hold a slot each, so two
// picks in a row cannot both see room for the same six pictures.
let pendingUploads = 0;

/**
 * One file at a time rather than in parallel: each is decoded and re-encoded
 * through a canvas, and a handful of large photos at once is enough to lock
 * up the page for a noticeable moment.
 */
export async function addImages(list: FileList | File[] | null): Promise<void> {
  if (busy.value || !status.value.configured) return;

  const files = Array.from(list ?? []).filter((file) => file.type.startsWith('image/'));
  if (!files.length) return;
  // Said rather than silent: the picker and the paste offered this, and a
  // refused picture with no word about it looks like a broken app.
  if (!status.value.vision) {
    setFlash(t('imagesUnsupported'));
    return;
  }

  const room = MAX_IMAGES - attachments.value.length - pendingUploads;
  if (files.length > room) setFlash(t('tooManyImages', { count: MAX_IMAGES }));
  if (room <= 0) return;

  const account = generation;
  for (const file of files) {
    // Checked and counted with no await in between, so no other call can take
    // the last slot between the check and the increment.
    if (attachments.value.length + pendingUploads >= MAX_IMAGES) break;
    pendingUploads += 1;
    try {
      let prepared: PreparedImage;
      try {
        prepared = await prepareImage(file);
      } catch (error) {
        setFlash(error instanceof ImageError && error.kind === 'too-large' ? t('imageTooLarge') : t('imageFailed'));
        continue;
      }
      // Signed out while the picture was decoded: it is not uploaded under the
      // next account's session, which would be the wrong person's file.
      if (account !== generation) {
        URL.revokeObjectURL(prepared.previewURL);
        return;
      }
      try {
        const { attachment } = await uploadAttachment({
          mime: prepared.mime,
          data: prepared.data,
          width: prepared.width,
          height: prepared.height,
        });
        if (account !== generation) {
          URL.revokeObjectURL(prepared.previewURL);
          return;
        }
        attachments.value = [...attachments.value, { ref: attachment, preview: prepared.previewURL }];
      } catch (error) {
        URL.revokeObjectURL(prepared.previewURL);
        setFlash(error instanceof ApiError ? apiErrorText(error) : t('imageFailed'));
      }
    } finally {
      pendingUploads -= 1;
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

  const account = generation;
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
    // The file was read by the account that has since signed out; its text
    // stays out of the next account's draft.
    if (account !== generation) return;

    // A binary file read as text comes back full of replacement characters;
    // sending that wastes tokens and tells the model nothing.
    if (looksBinary(text)) {
      setFlash(t('fileUnsupported'));
      continue;
    }

    // The message the server receives is the whole draft, so a file is cut to
    // what is left after the text already there, not only to its own cap.
    // trimEnd, not /\s*$/: the regex retries from every position in a long run
    // of spaces, which is quadratic on a pasted block of them.
    const existing = draft.value.trimEnd();
    const prefix = existing ? existing.length + 2 : 0;
    const fence = fenceFor(text);
    const frame = (content: string): string =>
      `${file.name}:\n\n${fence}${fenceLanguage(file.name)}\n${content}\n${fence}\n`;
    const room = Math.min(MAX_FILE_CHARS, MAX_MESSAGE_CHARS - prefix - frame('').length);

    let body = text;
    let cut = false;
    if (body.length > room) {
      const keep = room - TRUNCATION_MARK.length;
      if (keep <= 0) {
        setFlash(t('fileTooLarge'));
        continue;
      }
      body = `${body.slice(0, keep)}${TRUNCATION_MARK}`;
      cut = true;
    }

    const block = frame(body);
    draft.value = existing ? `${existing}\n\n${block}` : block;
    setFlash(t(cut ? 'fileTruncated' : 'fileAdded', { name: file.name }));
  }
}

// --- turns ----------------------------------------------------------------------

interface TurnOptions {
  content?: string;
  attachmentIDs?: string[];
  /**
   * The pictures this turn carries, for the optimistic row. Not the composer's
   * staged list: an edit-and-resend carries the message's own pictures.
   */
  sentAttachments?: AttachmentRef[];
  truncateFrom?: string;
  conversationID?: string;
}

export const chatChallenge = ref<TurnOptions | null>(null);
export const chatChallengeError = ref('');

/**
 * `unsent` means the server never took the turn: it refused the request before
 * any stream opened. The text is put back into the composer, because nothing
 * was saved and the optimistic row is about to be replaced by the server copy.
 */
export async function runTurn(turn: TurnOptions, turnstile = ''): Promise<'done' | 'challenge' | 'unsent'> {
  if (busy.value || !status.value.configured) return 'done';
  recentReasoningID.value = '';
  const account = generation;
  const conversationID = turn.conversationID ?? activeID.value;

  // Optimistic local rewind, so the transcript reacts before the server
  // answers. The server does the same thing to the rows.
  if (turn.truncateFrom) {
    const cut = messages.value.findIndex((message) => message.id === turn.truncateFrom);
    if (cut >= 0) messages.value = messages.value.slice(0, cut);
  }
  if (turn.content || turn.attachmentIDs?.length) {
    const userMessage: Message = {
      id: `local-${Date.now()}`,
      seq: messages.value.length + 1,
      role: 'user',
      content: turn.content ?? '',
      attachments: turn.sentAttachments ?? [],
      created_at: Date.now(),
    };
    markFlying(userMessage.id);
    messages.value = messages.value.concat([userMessage]);
    justSentID.value = userMessage.id;
  }

  busy.value = true;
  editingID.value = '';
  pending.value = { answer: '', reasoning: '', startedAt: Date.now() };
  // The turn belongs to the screen it was sent from, and to the conversation
  // that screen is showing — which may still be unnamed for one more moment.
  pendingView.value = switchTick.value;
  pendingID.value = conversationID;
  let turnID = conversationID;
  setFlash('');
  scrollToEnd();
  // justSentID deliberately stays set for the turn. Clearing it here, in the
  // same tick as the assignment above, meant neither value was ever painted
  // and the sent animation in ChatMessage never played at all. What it names
  // stops being on screen when the screen moves to another conversation or
  // resets, and those are where it is cleared.

  // The turn's own controller: resetChat clears the shared one on sign-out,
  // and this function still has to read its signal afterwards.
  const turnController = new AbortController();
  controller.value = turnController;
  let failed = false;
  // Set once the server has taken the turn. After that the user message exists
  // on the server, so a failure must not hand the text back to the composer.
  let accepted = false;
  let unsent = false;

  try {
    await sendTurn(
      {
        ...(conversationID ? { conversation_id: conversationID } : {}),
        model_id: status.value.modelID,
        ...(turn.content ? { content: turn.content } : {}),
        ...(turn.attachmentIDs?.length ? { attachment_ids: turn.attachmentIDs } : {}),
        ...(turn.truncateFrom ? { truncate_from_message_id: turn.truncateFrom } : {}),
        // Only on the turn that opens the conversation. On a later one the
        // server ignores it anyway, and sending a stale one would read as an
        // attempt to move a thread after the fact.
        ...(conversationID || !pendingProjectID.value ? {} : { project_id: pendingProjectID.value }),
        reasoning: {
          enabled: status.value.reasoningAvailable && status.value.reasoningEnabled,
          effort: status.value.reasoningEffort,
        },
        ...(turnstile ? { turnstile } : {}),
      },
      {
        onStart: (payload) => {
          accepted = true;
          turnID = payload.conversation_id;
          pendingID.value = turnID;
          // Adopting the new id is how the first message of a conversation
          // lands in it — but only if the reader is still on the screen that
          // sent it. Having wandered off, being pulled back is not a fix.
          if (!activeID.value && watching()) activeID.value = turnID;
          upsertConversationStub(payload.conversation_id, payload.title);
        },
        onDelta: (text) => bufferDelta(text),
        onReasoning: (text) => bufferDelta('', text),
        onDone: (payload) => {
          // A notice about this turn is only readable where the turn is. Said
          // over a conversation the reader switched to, it would be about
          // something they cannot see.
          if (!watching()) return;
          recentReasoningID.value = payload.message_id;
          if (payload.stream_fallback) setFlash(t('streamFallback', { reason: payload.stream_fallback }));
          else if (payload.stopped) setFlash(t('stopped'));
        },
        onError: (payload) => {
          failed = true;
          // An error before `start` can only be the server rolling back its own
          // opening transaction, so nothing was saved and the text can go back
          // to the composer, as a refused request does.
          if (!accepted) unsent = true;
          // Not lost when the reader is elsewhere: the server writes an error
          // row, so opening the conversation shows what went wrong.
          // The server's sentence is English and is only the fallback: a code the
          // interface has a reader's version of is shown in the reader's language.
          if (watching()) setFlash(storedErrorText(payload.code, payload.message));
        },
      },
      turnController.signal,
    );
  } catch (error) {
    if (!turnController.signal.aborted) {
      if (error instanceof ApiError &&
          (error.code === 'chat_challenge_required' || error.code === 'challenge_failed' ||
           error.code === 'challenge_unavailable')) {
        failed = true;
        // The server rejects before creating a turn. Remove the optimistic
        // copy so solving or cancelling the challenge cannot duplicate text.
        const last = messages.value[messages.value.length - 1];
        if ((turn.content || turn.attachmentIDs?.length) && last?.id.startsWith('local-')) {
          messages.value = messages.value.slice(0, -1);
        }
        chatChallenge.value = { ...turn, conversationID };
        chatChallengeError.value = error.code === 'chat_challenge_required' ? '' : error.message;
      } else {
        failed = true;
        // The server commits the user's message before it sends `start`, so a
        // dropped connection or an abort in between leaves the message on the
        // server. Only a refusal answered with a status, before the stream
        // opened, means the turn was never taken.
        if (!accepted && error instanceof ApiError && error.status > 0) unsent = true;
        if (watching()) setFlash(failureText(error));
      }
    }
  } finally {
    settleDeltas();
    if (controller.value === turnController) controller.value = null;
  }
  // What the reader was looking at when the stream ended. Read after
  // settleDeltas so the last buffered text is in it.
  const partial = pending.value;
  const stoppedWithText = turnController.signal.aborted &&
    !!(partial?.answer.trim() || partial?.reasoning.trim());

  // A sign-out ended this turn: resetChat already cleared the transcript, the
  // busy flag and the pending block, and the next account may have started a
  // turn of its own since. Nothing here may touch that.
  if (account !== generation) return 'done';

  try {
    // Keep the streamed block on screen until the saved row is ready. Clearing
    // it before the read left an empty answer for the duration of that request.
    // Only reload the conversation that owns this turn: the reader may have
    // moved on while it was streaming.
    if (activeID.value === turnID) {
      if (stoppedWithText) await reloadUntilSaved(turnID);
      else await reloadActive();
      scrollToEnd();
    }
  } finally {
    busy.value = false;
    pending.value = null;
    pendingID.value = '';
  }
  if (!failed) setFlash(flash.value);
  void refreshList();
  if (chatChallenge.value) return 'challenge';
  if (unsent) {
    if (turn.content) draft.value = draft.value.trim() ? `${turn.content}\n\n${draft.value}` : turn.content;
    return 'unsent';
  }
  return 'done';
}

/** Drops the composer's staged pictures that a turn carried, and no others. */
function releaseAttachments(ids: string[]): void {
  const sent = new Set(ids);
  for (const entry of attachments.value) {
    if (sent.has(entry.ref.id)) URL.revokeObjectURL(entry.preview);
  }
  // A picture whose upload finished while the turn was streaming was not part
  // of it, and stays staged for the next message.
  attachments.value = attachments.value.filter((entry) => !sent.has(entry.ref.id));
}

export async function submit(): Promise<void> {
  if (busy.value || !status.value.configured) return;

  const text = draft.value.trim();
  const staged = attachments.value.map((entry) => entry.ref);
  const ids = staged.map((ref) => ref.id);
  if (!text && !ids.length) return;

  draft.value = '';
  const result = await runTurn({ content: text, attachmentIDs: ids, sentAttachments: staged });

  // A challenge keeps the text and the pictures for when it is solved, and a
  // refused turn keeps them in the composer for another try.
  if (result !== 'done') return;
  releaseAttachments(ids);
}

export async function completeChatChallenge(token: string): Promise<void> {
  const turn = chatChallenge.value;
  if (!turn || !token) return;
  chatChallenge.value = null;
  chatChallengeError.value = '';
  const result = await runTurn(turn, token);
  if (result !== 'done') return;
  releaseAttachments(turn.attachmentIDs ?? []);
}

export function cancelChatChallenge(): void {
  const turn = chatChallenge.value;
  if (turn?.content && !draft.value.trim()) draft.value = turn.content;
  chatChallenge.value = null;
  chatChallengeError.value = '';
}

/**
 * Aborting the fetch closes the connection, which cancels the server's
 * request context, which cancels the provider call. One line, and the
 * upstream stops billing.
 */
export function stop(): void {
  controller.value?.abort();
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
  const account = generation;
  try {
    const { conversations: list } = await listConversations({ limit: RAIL_LIMIT });
    if (account === generation) conversations.value = list;
  } catch {
    // The rail is a convenience; a failed refresh leaves the last copy.
  }
}

async function reloadActive(): Promise<void> {
  const id = activeID.value;
  if (!id) {
    messages.value = [];
    return;
  }
  try {
    const { messages: list, conversation } = await getConversation(id);
    // Switching no longer waits for a turn, so a reload can land after the
    // reader has moved on. Rows belong to the conversation they were read
    // from, never to whatever happens to be on screen when they arrive.
    if (activeID.value !== id) return;
    messages.value = list;
    // Mirrored so the project rail marks the project the open thread lives
    // in. It cannot move the thread: runTurn sends it only on the turn that
    // opens a conversation, and leaving this screen for a new chat resets it.
    if (conversation) pendingProjectID.value = conversation.project_id || '';
  } catch (error) {
    if (error instanceof ApiError && error.status === 404) {
      if (activeID.value === id) dropActive();
      return;
    }
    // The transcript is empty after a failure, which reads as a conversation
    // with nothing in it. Said only while the reader is still on it: a failure
    // for a conversation they have left is not theirs to see.
    if (activeID.value === id) setFlash(failureText(error));
  }
}

/**
 * A stopped answer is saved by the server after the connection that asked for
 * it has closed, and the client gets no signal when that write lands, so read
 * until the row shows up. The schedule is bounded (about 2.2 s): past it the
 * composer is released anyway, rather than held on a save that never comes.
 */
async function reloadUntilSaved(id: string): Promise<void> {
  for (const delay of [0, 150, 300, 600, 1200]) {
    if (delay) await new Promise((resolve) => setTimeout(resolve, delay));
    if (activeID.value !== id) return;
    await reloadActive();
    if (activeID.value !== id) return;
    if (messages.value[messages.value.length - 1]?.role === 'assistant') return;
  }
}

/**
 * Reading is not blocked by writing: a turn keeps streaming into its own
 * conversation while the reader goes through another one. There is still only
 * one turn at a time — the composer stays busy until it ends — but waiting for
 * an answer is no longer waiting to look at anything else.
 */
export async function openConversation(id: string): Promise<void> {
  // Selecting the conversation already on screen changes nothing. Clearing the
  // transcript here blanked it for a reload, and a double-click on a row (which
  // is how a rename starts) selects it twice. An empty transcript is still
  // loaded, so a failed read can be tried again by selecting it.
  if (id && id === activeID.value && messages.value.length > 0) {
    historyOpen.value = false;
    return;
  }
  activeID.value = id;
  editingID.value = '';
  justSentID.value = '';
  recentReasoningID.value = '';
  historyOpen.value = false;
  messages.value = [];
  resetFlightState();
  // A short rise says "a different conversation" instead of leaving the
  // transcript to flicker into something else within one frame.
  switchTick.value += 1;
  // Coming back to the conversation that is still writing re-attaches the
  // streamed block: it is the same turn, on screen again.
  if (id && id === pendingID.value) pendingView.value = switchTick.value;
  await reloadActive();
  scrollToEnd();
}

export function startNewConversation(): void {
  leaveForNewConversation('');
}

export function startProjectConversation(projectID: string): void {
  leaveForNewConversation(projectID);
}

/**
 * The one way to an empty composer, so "new chat" and "new chat in a project"
 * cannot drift apart: they differ only in where the next conversation opens.
 */
function leaveForNewConversation(projectID: string): void {
  activeID.value = '';
  pendingProjectID.value = projectID;
  messages.value = [];
  editingID.value = '';
  justSentID.value = '';
  recentReasoningID.value = '';
  historyOpen.value = false;
  resetFlightState();
  // A running turn already holds its own copy of what it sent, so this drops
  // only what is still staged in the composer — and nothing can be staged
  // while a turn is running.
  clearAttachments();
  switchTick.value += 1;
}

/**
 * The open conversation stopped existing for this reader — archived, deleted,
 * or gone on the server. Not a switch: no rise, and the composer keeps what is
 * staged in it. But a turn still streaming into that conversation must stop
 * drawing here, or its block keeps writing over the blank screen until the
 * turn ends. Detaching the view does that and nothing else; the turn still
 * finishes, and the server still saves it.
 */
function dropActive(): void {
  activeID.value = '';
  messages.value = [];
  if (watching()) pendingView.value = -1;
}

/**
 * Renames from the rail's inline field — not window.prompt, which some
 * browsers and the preview pane answer with null before anyone sees it. The
 * new title shows at once, since the field it was typed into has already
 * closed; a refusal puts the old one back and says why. An empty or unchanged
 * name is not a rename, so nothing is sent.
 */
export async function rename(conversation: Conversation, title: string): Promise<void> {
  const next = title.trim();
  if (!next || next === conversation.title) return;
  // The rail and the header read their own copies, which are not the objects
  // a project row was given, so the rail's entry is renamed as well.
  const listed = conversations.value.find((entry) => entry.id === conversation.id);
  const before = conversation.title;
  const listedBefore = listed?.title;
  conversation.title = next;
  if (listed) listed.title = next;
  try {
    const { conversation: updated } = await renameConversation(conversation.id, next);
    conversation.title = updated.title;
    if (listed) listed.title = updated.title;
  } catch (error) {
    // Only undo what this rename wrote: a later rename made while this one
    // was in flight owns the title now, and its own outcome decides it.
    if (conversation.title === next) conversation.title = before;
    if (listed && listedBefore !== undefined && listed.title === next) listed.title = listedBefore;
    setFlash(failureText(error));
  }
}

/** True only when the server accepted it and the rail has been updated. */
export async function archiveConversation(conversation: Conversation): Promise<boolean> {
  try {
    await archiveConversationApi(conversation.id, true);
  } catch (error) {
    setFlash(failureText(error));
    return false;
  }
  conversations.value = conversations.value.filter((entry) => entry.id !== conversation.id);
  if (activeID.value === conversation.id) dropActive();
  setFlash(t('archiveSuccess'));
  return true;
}

// Resolves to whether the conversation left the archive. The failure is also a
// flash, but a caller that drops the row from its own list needs the answer, not
// the message on screen.
export async function unarchiveConversation(id: string): Promise<boolean> {
  try {
    await archiveConversationApi(id, false);
    await refreshList();
    setFlash(t('unarchiveSuccess'));
    return true;
  } catch (error) {
    setFlash(failureText(error));
    return false;
  }
}

/** True only when the server deleted it and the rail has been updated. */
export async function removeConversation(conversation: Conversation): Promise<boolean> {
  try {
    await deleteConversation(conversation.id);
  } catch (error) {
    setFlash(failureText(error));
    return false;
  }
  conversations.value = conversations.value.filter((entry) => entry.id !== conversation.id);
  if (activeID.value === conversation.id) dropActive();
  return true;
}

export async function clearEverything(): Promise<void> {
  if (!conversations.value.length) return;
  const account = generation;
  try {
    await deleteAllConversations();
  } catch (error) {
    // A failure that comes back after sign-out is the account that left's.
    if (account === generation) setFlash(failureText(error));
    return;
  }
  // Signed out while the delete was in flight. Whoever is signed in now may hold
  // a rail that predates the delete (the same account's sign-in read can), so it
  // is read again rather than cleared.
  if (account !== generation) {
    if (currentUser.value) void refreshList();
    return;
  }
  conversations.value = [];
  activeID.value = '';
  messages.value = [];
}

/**
 * Clears everything one account left on screen: the open conversation, the
 * draft, what is staged, the project it was pointed into, and a turn that is
 * still streaming. Leaving the chat mid-generation aborts the turn; the server
 * still saves whatever streamed before that.
 */
export function resetChat(): void {
  generation += 1;
  controller.value?.abort();
  controller.value = null;
  // Deltas buffered for the turn being cut off must not land in the pending
  // block of whichever turn runs next.
  if (frame) cancelAnimationFrame(frame);
  frame = 0;
  bufferedAnswer = '';
  bufferedReasoning = '';
  // Whatever the cut-off turn still reports is for a screen that is gone.
  pendingView.value = -1;
  resetFlightState();
  clearAttachments();
  conversations.value = [];
  messages.value = [];
  activeID.value = '';
  pendingProjectID.value = '';
  editingID.value = '';
  justSentID.value = '';
  recentReasoningID.value = '';
  historyOpen.value = false;
  draft.value = '';
  flash.value = '';
  chatChallenge.value = null;
  chatChallengeError.value = '';
  pending.value = null;
  pendingID.value = '';
  busy.value = false;
  suggestions.value = pickSuggestions(SUGGESTIONS_SHOWN);
}

// session.ts cannot import this module, which imports it, so the store
// registers its own reset and every sign-out runs it.
onSignOut(resetChat);

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

/**
 * A fence one backtick longer than any run that starts a line of the file.
 * A file with its own three-backtick line would close the wrapper early, and
 * everything after it would be read as prose.
 */
function fenceFor(body: string): string {
  let longest = 2;
  for (const match of body.matchAll(/^[ \t]*(`+)/gm)) longest = Math.max(longest, match[1]!.length);
  return '`'.repeat(longest + 1);
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
  // A file named `x.constructor` or `x.__proto__` would otherwise read
  // Object's own members as a language tag.
  return Object.hasOwn(known, extension) ? known[extension]! : '';
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
