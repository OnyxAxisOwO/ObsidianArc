// One terminal tab's state.
//
// A plain factory returning refs, not a store — each tab owns one of these,
// nothing here is process-wide except the shared command history, and there
// is no Pinia in this project to reach for. Nothing in this file touches the
// DOM: column width comes in through `getColumns()`, history persistence
// through `localStorage`, and every side effect a caller can observe is a
// change to one of the returned refs.

import { ref, watch, type Ref } from 'vue';
import { completeConsoleLine, execConsoleLine, type ConsoleCompletion } from '@/api/console';
import { t } from '@/composables/useI18n';
import { currentUser } from '@/stores/session';
import { createAnsiTokenizer, initialAnsiState, tokenizeAnsiChunk, type AnsiToken } from './ansi';

type ScrollbackBlockKind = 'banner' | 'echo' | 'output' | 'error';

/** One line the reader submitted, or one chunk of what came back for it. */
export interface ScrollbackBlock {
  id: number;
  kind: ScrollbackBlockKind;
  tokens: AnsiToken[];
  timestamp: number;
}

interface RunOutcome {
  ok: boolean;
  code: string;
  exit: boolean;
}

type RecallDirection = 'up' | 'down';

// --- history: shared across tabs, one module-level store -------------------
//
// This persists across tabs while a tab's scrollback does not, so it cannot
// live inside the per-tab factory below —
// it is created once, when this module first loads, and every session
// shares the one ref.

/**
 * Stored per account, under this prefix plus the account's id. The bare
 * prefix was the key when one list served whoever used the browser, so it is
 * also what is swept away on the way in. `stores/session.ts` clears everything
 * that starts with it when somebody signs out and spells the prefix out
 * itself, because importing it from here would drag this chunk into the main
 * one; a test holds the two spellings together.
 */
export const TERMINAL_HISTORY_KEY_PREFIX = 'obsidian-arc-terminal-history';
const HISTORY_CAP = 500;

/**
 * Whether a line carries something that must not be kept: a credential given
 * as a flag, or as an argument to a command that takes one that way. Judged
 * from the text alone, and on the safe side — a line wrongly kept out of
 * history costs a retype, one wrongly kept is a password in localStorage.
 *
 * The regex covers only flag names that are credential-shaped (passwords,
 * secrets, tokens, API keys, headers, codes, credentials). The server masks a wider set
 * marked Sensitive, but only in its audit line, and most of that wider set is
 * not a credential, so those flags stay in history.
 */
const SENSITIVE_FLAG = /(?:^|\s)-{1,2}[\w-]*(?:pass(?:word|wd)?|secret|token|api-?key|header|code|turnstile|credential)[\w-]*(?=$|[\s=])/i;
// `user passwd bob hunter2` carries a password as a bare word with no flag for
// SENSITIVE_FLAG to catch. The console masks its tail on the server, so the
// line is kept out of history whole. `watch` is not listed: it only repeats
// another command, which is matched here by its own words, so `watch usage rpm`
// stays in history like any other line.
const SENSITIVE_COMMAND =
  /(?:^|\s)(?:setting\s+import|2fa\s+(?:enable|disable|recovery|backoffice)|me\s+invite\s+claim|me\s+passwd|backup\s+import|login-bg\s+set|logo\s+set|user\s+passwd)(?=\s|$)/i;
// `user create <name> <password>`: the console declares only the name, so the
// second positional is the secret, and the server masks it for the same reason.
const SENSITIVE_POSITIONAL = /(?:^|\s)user\s+create\s+\S+\s+[^\s-]/i;
const SECRET_SETTING = /(?:^|\s)setting\s+set\s+\S*(?:secret|pass(?:word|wd)|token|key|credential)/i;

/**
 * Quotes and backslashes are stripped before matching, as the server's own
 * tokeniser strips them before it reads a line: `'--password'` and
 * `'user passwd' bob` are the same command to the console, so they must be the
 * same line here. Stripping is not always the safe direction, though: it turns
 * `"-hunter2"` into a dash-led token the positional rule's lookahead rejects,
 * so that rule is also run on the raw line. The stripped text is still needed
 * for quoted command words, as in `"user" create bob hunter2`.
 */
export function isSensitiveTerminalLine(line: string): boolean {
  const text = line.replace(/["'\\]/g, '');
  return (
    SENSITIVE_FLAG.test(text) ||
    SENSITIVE_COMMAND.test(text) ||
    SENSITIVE_POSITIONAL.test(text) ||
    SENSITIVE_POSITIONAL.test(line) ||
    SECRET_SETTING.test(text)
  );
}

/**
 * What the scrollback shows for a line that must not be kept: its command
 * words, then `***`. The echo is the one copy of the typed text that would
 * otherwise stay on screen for the life of the tab; the server's audit line
 * masks the same tail.
 */
function redactTerminalEcho(line: string): string {
  const words = line.trim().split(/\s+/);
  const shown: string[] = [];
  for (const word of words) {
    // A flag is where a value may start, so the words stop before it.
    if (shown.length === 2 || word.startsWith('-')) break;
    shown.push(word);
  }
  return shown.length === words.length ? shown.join(' ') : [...shown, '***'].join(' ');
}

/**
 * Whether the line is the `lang` command. The console looks a command up by
 * its exact first word. Quotes are dropped before the comparison because the
 * console reads `"lang"` as the word `lang`, so `"lang" zh` counts too.
 */
function isLangCommand(line: string): boolean {
  const first = line.trim().split(/\s+/)[0] ?? '';
  return first.replace(/["'\\]/g, '') === 'lang';
}

function utf8Length(text: string): number {
  return new TextEncoder().encode(text).length;
}

/** The UTF-16 index of a UTF-8 byte offset into `line`; an offset past the end is the end. */
function utf16IndexOfByteOffset(line: string, byteOffset: number): number {
  let bytes = 0;
  let index = 0;
  for (const ch of line) {
    if (bytes >= byteOffset) break;
    bytes += utf8Length(ch);
    index += ch.length;
  }
  return index;
}

function historyKey(userId: string | null): string | null {
  return userId ? `${TERMINAL_HISTORY_KEY_PREFIX}:${userId}` : null;
}

function loadHistory(userId: string | null): string[] {
  const key = historyKey(userId);
  if (key === null) return [];
  try {
    const raw = localStorage.getItem(key);
    if (raw === null) return [];
    const parsed: unknown = JSON.parse(raw);
    if (!Array.isArray(parsed)) return [];
    const lines = parsed.filter((entry): entry is string => typeof entry === 'string' && !isSensitiveTerminalLine(entry));
    return lines.length > HISTORY_CAP ? lines.slice(lines.length - HISTORY_CAP) : lines;
  } catch {
    return [];
  }
}

function persistHistory(userId: string | null, lines: readonly string[]): void {
  const key = historyKey(userId);
  if (key === null) return;
  try {
    localStorage.setItem(key, JSON.stringify(lines));
  } catch {
    // Best effort; history still works for the rest of this page's life.
  }
}

try {
  // Older builds kept one history list for every account on this browser.
  // Nothing writes it now; this sweep can go once no supported release still
  // writes it.
  localStorage.removeItem(TERMINAL_HISTORY_KEY_PREFIX);
} catch {
  // Storage may be unavailable; there is then nothing to sweep either.
}

const sharedHistory: Ref<string[]> = ref([]);

// Follows the signed-in account: one account's lines never show up under
// another's prompt, and signing out leaves nothing in memory to recall. Sync,
// so the list is already the new account's by the time sign-in resolves.
watch(
  () => currentUser.value?.id ?? null,
  (userId) => {
    sharedHistory.value = loadHistory(userId);
  },
  { immediate: true, flush: 'sync' },
);

/** Appends one submitted line, collapsing an immediate repeat and capping the length — the shape every shell's history file uses, and why two tabs running the same command back to back do not double it. */
function pushHistory(line: string): void {
  const trimmed = line.trim();
  if (!trimmed) return;
  // Not kept at all, not even for this page's life: a line recalled with the
  // arrow keys is one a shoulder or a screen share can read.
  if (isSensitiveTerminalLine(trimmed)) return;
  const list = sharedHistory.value;
  if (list.length > 0 && list[list.length - 1] === trimmed) return;
  const next = list.length >= HISTORY_CAP ? [...list.slice(list.length - HISTORY_CAP + 1), trimmed] : [...list, trimmed];
  sharedHistory.value = next;
  persistHistory(currentUser.value?.id ?? null, next);
}

// --- per-tab session ---------------------------------------------------------

export interface TerminalSession {
  readonly id: string;
  readonly title: Ref<string>;
  /** The prompt's unsent text. The component's `<textarea>` binds to this directly, which is what lets `recall()` rewrite it without the component passing its current value in on every keystroke. */
  readonly draft: Ref<string>;
  readonly scrollback: Ref<ScrollbackBlock[]>;
  readonly running: Ref<boolean>;
  /** Runs one line. Resolves with the command's verdict, or a synthetic `{ ok: false, code: 'cancelled' }` when `cancel()` won the race before a `done` event arrived. Never rejects — a transport failure is appended to the scrollback as an error block instead, the same as a command's own failure would be. */
  run(line: string): Promise<RunOutcome>;
  /** Aborts the in-flight command, if any. A no-op otherwise. */
  cancel(): void;
  /** Walks command history into `draft`, saving the in-progress draft on the first `'up'` and restoring it once `'down'` walks back past the newest entry — the same model a shell's readline uses. */
  recall(direction: RecallDirection): void;
  /** `pos` and the returned `from` are UTF-16 indexes into `line`, the units the draft is held in; the server's byte offsets are converted here and nowhere else. */
  complete(line: string, pos: number): Promise<ConsoleCompletion>;
  /** `Ctrl-L`: an instant, local clear — no round trip, unlike the server's own `clear` command, whose ANSI clear sequence arrives through the normal `run()` path and is handled identically. */
  clearScrollback(): void;
  /** Applied when the scrollback pref changes, so an open tab picks up a smaller cap immediately rather than waiting for its next block. */
  setScrollbackCap(cap: number): void;
  /** Aborts the in-flight command. Call from the component's `onBeforeUnmount` / tab-close handler — an `exec` stream left running after teardown would keep calling handlers that write into state Vue has already discarded. */
  dispose(): void;
}

interface CreateTerminalSessionOptions {
  id: string;
  /** Empty means "no custom title yet" — the component supplies the default (localized) label to show instead; this module never holds an English string. */
  title?: string | undefined;
  /** `Spec.banner`, shown as the pane's first block. Omitted or empty means no banner block. */
  banner?: string | undefined;
  scrollbackCap: number;
  /** Read fresh at the start of every `run()`, so a pane resized mid-session sends its current width, not the width the tab was created at. The measurement itself stays in the component — this module never touches the DOM. */
  getColumns: () => number;
}

/**
 * How many ANSI tokens one scrollback block holds before the next one starts.
 * Small enough that a long-running command is trimmed promptly, large enough
 * that an ordinary table is still one block.
 */
const MAX_TOKENS_PER_BLOCK = 400;

/**
 * Every token is one span the page mounts, and a coloured listing can put 400
 * of them in one block, so the block count alone allowed millions of nodes at
 * the largest setting. Ordinary output sits far under this: a plain line is
 * one or two tokens, so the line setting still decides what is kept and this
 * only trims heavily coloured output.
 *
 * Module scope: the factory pushes the banner, and that push trims against
 * this budget, so a `const` further down the factory would not be set yet.
 */
const MAX_RETAINED_TOKENS = 50_000;

export function createTerminalSession(options: CreateTerminalSessionOptions): TerminalSession {
  const id = options.id;
  const title = ref(options.title ?? '');
  const draft = ref('');
  const scrollback: Ref<ScrollbackBlock[]> = ref([]);
  const running = ref(false);

  let scrollbackCap = options.scrollbackCap;
  let nextBlockId = 0;
  let activeController: AbortController | null = null;
  let currentOutputBlock: ScrollbackBlock | null = null;
  let historyIndex = -1; // -1 means "not browsing history, draft is live"
  let savedDraft = '';
  // Tokens held across every block, kept as a running total so trimming never
  // has to walk the scrollback to count them.
  let retainedTokens = 0;

  if (options.banner) {
    pushBlock('banner', tokenizeAnsiChunk(options.banner, initialAnsiState()).tokens);
  }

  function pushBlock(kind: ScrollbackBlockKind, tokens: AnsiToken[]): ScrollbackBlock {
    const block: ScrollbackBlock = { id: nextBlockId, kind, tokens, timestamp: Date.now() };
    nextBlockId += 1;
    scrollback.value.push(block);
    retainedTokens += tokens.length;
    trimScrollback();
    // Return the reactive proxy inside scrollback rather than the raw block,
    // so subsequent token appends to currentOutputBlock trigger Vue updates.
    return scrollback.value[scrollback.value.length - 1] ?? block;
  }

  function trimScrollback(): void {
    // Capped by block count, not by rendered line count: a block can hold
    // embedded newlines, and computing how those wrap needs the pane's own
    // column width, which this module never measures. Output also rolls over
    // to a new block every MAX_TOKENS_PER_BLOCK tokens, so one SSE chunk can span
    // several blocks and one block can hold several chunks; the total is
    // bounded by MAX_RETAINED_TOKENS.
    //
    // The newest block is never dropped: it is the one output is still being
    // written into.
    const blocks = scrollback.value;
    let drop = 0;
    let tokens = retainedTokens;
    while (drop < blocks.length - 1 && (blocks.length - drop > scrollbackCap || tokens > MAX_RETAINED_TOKENS)) {
      tokens -= blocks[drop]!.tokens.length;
      drop += 1;
    }
    if (drop > 0) {
      blocks.splice(0, drop);
      retainedTokens = tokens;
    }
  }

  function appendEcho(line: string): void {
    pushBlock('echo', [{ kind: 'text', text: line, cls: [] }]);
  }

  function appendError(message: string): void {
    pushBlock('error', [{ kind: 'text', text: message, cls: ['ansi-fg-31'] }]);
  }

  function appendOutput(tokens: AnsiToken[]): void {
    for (const token of tokens) {
      if (token.kind === 'clear') {
        // `watch` redraws a whole frame from nothing each tick, exactly as
        // running `clear` in a real terminal would.
        scrollback.value = [];
        retainedTokens = 0;
        currentOutputBlock = null;
        continue;
      }
      // Rolled over rather than grown without end. The cap counts blocks, and
      // a single streaming command — `watch`, or a large listing — used to
      // put everything into one block that the cap could never evict, so a
      // tab left on `watch` grew until the browser gave out. Starting a new
      // block every so often is what puts that output back under the cap.
      if (!currentOutputBlock || currentOutputBlock.tokens.length >= MAX_TOKENS_PER_BLOCK) {
        currentOutputBlock = pushBlock('output', []);
      }
      currentOutputBlock.tokens.push(token);
      retainedTokens += 1;
    }
    // Appended tokens can take the total over the budget without adding a
    // block, so the trim that pushBlock runs is not enough on its own here.
    trimScrollback();
  }

  /** Whether `format json` is in effect for this tab. See ConsoleExecDone. */
  let jsonFormat = false;
  /** The language `lang` last set for this tab; undefined means the UI language. */
  let lang: string | undefined;

  async function run(line: string): Promise<RunOutcome> {
    if (running.value) {
      // The transports run one command at a time per session; the component is
      // expected to disable submission while
      // `running` is true, but a stray call must not start a second stream.
      return { ok: false, code: 'busy', exit: false };
    }

    // The echo is the scrollback's own copy of what was typed, so a line kept
    // out of history is not kept here either: its secret would stay on screen
    // for the life of the tab. The raw line still goes to the server below.
    appendEcho(isSensitiveTerminalLine(line) ? redactTerminalEcho(line) : line);
    draft.value = '';
    historyIndex = -1;
    savedDraft = '';

    if (line.trim().length === 0) {
      // An empty Enter is a no-op prompt, the same as a real shell.
      return { ok: true, code: '', exit: false };
    }

    pushHistory(line);

    const controller = new AbortController();
    activeController = controller;
    running.value = true;
    currentOutputBlock = null;
    const tokenizer = createAnsiTokenizer();
    // Cast rather than annotated: the handlers below assign it, and TypeScript
    // does not see assignments made inside a callback, so an annotated `null`
    // would be narrowed to null for good and `outcome !== null` would read as
    // never after the stream ends.
    let outcome = null as RunOutcome | null;
    // Only `lang` changes the language a tab carries. The done frame echoes the
    // session language after every command, so a comparison against what was
    // sent cannot tell a `lang` that set the language it already had from an
    // echo: the change was dropped whenever the UI language matched it.
    const setsLang = isLangCommand(line);

    try {
      await execConsoleLine(
        { line, cols: options.getColumns(), json: jsonFormat, ...(lang ? { lang } : {}) },
        {
          onOut: (text) => appendOutput(tokenizer.push(text)),
          onDone: (payload) => {
            // `format json` and `lang` are session settings, and this is where
            // a tab learns that they changed.
            jsonFormat = payload.json;
            // A refused `lang` answers with the language it was sent with, and
            // adopting that would pin the tab to the UI language for good.
            if (setsLang && payload.ok && payload.lang) lang = payload.lang;
            outcome = { ok: payload.ok, code: payload.code, exit: payload.exit };
          },
          onError: (payload) => {
            appendError(payload.message);
            outcome = { ok: false, code: payload.code, exit: false };
          },
        },
        controller.signal,
      );
      // The stream ended with neither a done nor an error frame, so the
      // connection dropped mid-command. Without this the spinner clears as if
      // the command had stopped by itself.
      if (outcome === null && !controller.signal.aborted) {
        appendError(t('streamInterrupted'));
        outcome = { ok: false, code: 'stream', exit: false };
      }
    } catch (error) {
      if (!controller.signal.aborted) {
        appendError(error instanceof Error ? error.message : String(error));
        outcome = { ok: false, code: 'stream', exit: false };
      }
    } finally {
      // Closes the request on every exit path. A handler that throws leaves the
      // stream open otherwise, and the server keeps generating output for a tab
      // that has stopped listening; after a normal finish this is a no-op.
      controller.abort();
      running.value = false;
      if (activeController === controller) activeController = null;
      currentOutputBlock = null;
    }

    return outcome ?? { ok: false, code: 'cancelled', exit: false };
  }

  function cancel(): void {
    activeController?.abort();
  }

  function recall(direction: RecallDirection): void {
    const list = sharedHistory.value;
    if (direction === 'up') {
      if (historyIndex === -1) {
        if (list.length === 0) return;
        savedDraft = draft.value;
        historyIndex = list.length - 1;
      } else if (historyIndex > 0) {
        historyIndex -= 1;
      }
      draft.value = list[historyIndex] ?? '';
    } else {
      if (historyIndex === -1) return; // already at the live draft
      if (historyIndex >= list.length - 1) {
        historyIndex = -1;
        draft.value = savedDraft;
      } else {
        historyIndex += 1;
        draft.value = list[historyIndex] ?? '';
      }
    }
  }

  async function complete(line: string, pos: number): Promise<ConsoleCompletion> {
    // The server counts UTF-8 bytes. Sending the caret's UTF-16 index instead
    // lands inside a multi-byte character as soon as a CJK argument precedes
    // the caret, so the server completes against the wrong word.
    try {
      const result = await completeConsoleLine(line, utf8Length(line.slice(0, pos)), lang);
      return { from: utf16IndexOfByteOffset(line, result.from), items: result.items };
    } catch {
      // Tab-completion is a convenience, not the command itself; a network
      // hiccup should leave the line alone, not surface as an error block.
      return { from: pos, items: [] };
    }
  }

  function clearScrollback(): void {
    scrollback.value = [];
    retainedTokens = 0;
    currentOutputBlock = null;
  }

  function setScrollbackCap(cap: number): void {
    scrollbackCap = cap;
    trimScrollback();
  }

  function dispose(): void {
    activeController?.abort();
  }

  return {
    id,
    title,
    draft,
    scrollback,
    running,
    run,
    cancel,
    recall,
    complete,
    clearScrollback,
    setScrollbackCap,
    dispose,
  };
}
