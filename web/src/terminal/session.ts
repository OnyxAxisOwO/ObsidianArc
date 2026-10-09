// One terminal tab's state: CONTRACT.md #5.1/#5.2.
//
// A plain factory returning refs, not a store — each tab owns one of these,
// nothing here is process-wide except the shared command history, and there
// is no Pinia in this project to reach for. Nothing in this file touches the
// DOM: column width comes in through `getColumns()`, history persistence
// through `localStorage`, and every side effect a caller can observe is a
// change to one of the returned refs.

import { ref, watch, type Ref } from 'vue';
import { completeConsoleLine, execConsoleLine, type ConsoleCompletion } from '@/api/console';
import { currentUser } from '@/stores/session';
import { createAnsiTokenizer, initialAnsiState, tokenizeAnsiChunk, type AnsiToken } from './ansi';

export type ScrollbackBlockKind = 'banner' | 'echo' | 'output' | 'error';

/** One line the reader submitted, or one chunk of what came back for it. */
export interface ScrollbackBlock {
  id: number;
  kind: ScrollbackBlockKind;
  tokens: AnsiToken[];
  timestamp: number;
}

export interface RunOutcome {
  ok: boolean;
  code: string;
  exit: boolean;
}

export type RecallDirection = 'up' | 'down';

// --- history: shared across tabs, one module-level store -------------------
//
// CONTRACT.md #5.2 is explicit that this persists across tabs while a tab's
// scrollback does not, so it cannot live inside the per-tab factory below —
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
 * The flag names are the ones the console marks Sensitive plus anything that
 * reads like them, so a flag added later is covered without anyone having to
 * remember this file exists.
 */
const SENSITIVE_FLAG = /(?:^|\s)-{1,2}[\w-]*(?:pass(?:word|wd)?|secret|token|api-?key|header|code|turnstile|credential)[\w-]*(?=$|[\s=])/i;
const SENSITIVE_COMMAND =
  /(?:^|\s)(?:setting\s+import|2fa\s+(?:enable|disable|recovery|backoffice)|me\s+invite\s+claim|backup\s+import|login-bg\s+set|logo\s+set)(?=\s|$)/i;
const SECRET_SETTING = /(?:^|\s)setting\s+set\s+\S*(?:secret|pass(?:word|wd)|token|key|credential)/i;

export function isSensitiveTerminalLine(line: string): boolean {
  return SENSITIVE_FLAG.test(line) || SENSITIVE_COMMAND.test(line) || SECRET_SETTING.test(line);
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
  // The one list every account on this browser used to share.
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

/** Read-only view for anything that wants to list history without a tab (the `history` command's local echo, if a screen ever wants one). */
export function terminalHistorySnapshot(): readonly string[] {
  return sharedHistory.value;
}

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
  complete(line: string, pos: number): Promise<ConsoleCompletion>;
  /** `Ctrl-L`: an instant, local clear — no round trip, unlike the server's own `clear` command, whose ANSI clear sequence arrives through the normal `run()` path and is handled identically. */
  clearScrollback(): void;
  /** Applied when the scrollback pref changes, so an open tab picks up a smaller cap immediately rather than waiting for its next block. */
  setScrollbackCap(cap: number): void;
  /** Aborts the in-flight command. Call from the component's `onBeforeUnmount` / tab-close handler — an `exec` stream left running after teardown would keep calling handlers that write into state Vue has already discarded. */
  dispose(): void;
}

export interface CreateTerminalSessionOptions {
  id: string;
  /** Empty means "no custom title yet" — the component supplies the default (localized) label to show instead; this module never holds an English string. */
  title?: string | undefined;
  /** `Spec.banner`, shown as the pane's first block. Omitted or empty means no banner block. */
  banner?: string | undefined;
  scrollbackCap: number;
  /** Read fresh at the start of every `run()`, so a pane resized mid-session sends its current width, not the width the tab was created at. The measurement itself stays in the component — this module never touches the DOM. */
  getColumns: () => number;
}

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

  if (options.banner) {
    pushBlock('banner', tokenizeAnsiChunk(options.banner, initialAnsiState()).tokens);
  }

  function pushBlock(kind: ScrollbackBlockKind, tokens: AnsiToken[]): ScrollbackBlock {
    const block: ScrollbackBlock = { id: nextBlockId, kind, tokens, timestamp: Date.now() };
    nextBlockId += 1;
    scrollback.value.push(block);
    trimScrollback();
    // Return the reactive proxy inside scrollback rather than the raw block,
    // so subsequent token appends to currentOutputBlock trigger Vue updates.
    return scrollback.value[scrollback.value.length - 1] ?? block;
  }

  /**
   * How many ANSI tokens one scrollback block holds before the next one
   * starts. Small enough that a long-running command is trimmed promptly,
   * large enough that an ordinary table is still one block.
   */
  const maxTokensPerBlock = 400;

  function trimScrollback(): void {
    // Capped by block count, not by rendered line count: a block can hold
    // embedded newlines, and computing how those wrap needs the pane's own
    // column width, which this module never measures. One block is one
    // echoed line or one SSE `out` chunk, which in practice is the same
    // granularity a reader thinks of as "one thing that happened".
    const overflow = scrollback.value.length - scrollbackCap;
    if (overflow > 0) scrollback.value.splice(0, overflow);
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
        // running `clear` in a real terminal would — CONTRACT.md #1.7.
        scrollback.value = [];
        currentOutputBlock = null;
        continue;
      }
      // Rolled over rather than grown without end. The cap counts blocks, and
      // a single streaming command — `watch`, or a large listing — used to
      // put everything into one block that the cap could never evict, so a
      // tab left on `watch` grew until the browser gave out. Starting a new
      // block every so often is what puts that output back under the cap.
      if (!currentOutputBlock || currentOutputBlock.tokens.length >= maxTokensPerBlock) {
        currentOutputBlock = pushBlock('output', []);
      }
      currentOutputBlock.tokens.push(token);
    }
  }

  /** Whether `format json` is in effect for this tab. See ConsoleExecDone. */
  let jsonFormat = false;

  async function run(line: string): Promise<RunOutcome> {
    if (running.value) {
      // The transports run one command at a time per session (CONTRACT.md
      // #1.2); the component is expected to disable submission while
      // `running` is true, but a stray call must not start a second stream.
      return { ok: false, code: 'busy', exit: false };
    }

    appendEcho(line);
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
    let outcome: RunOutcome | null = null;

    try {
      await execConsoleLine(
        { line, cols: options.getColumns(), json: jsonFormat },
        {
          onOut: (text) => appendOutput(tokenizer.push(text)),
          onDone: (payload) => {
            // `format json` is a session setting, and this is where a tab
            // learns that it changed.
            jsonFormat = payload.json;
            outcome = { ok: payload.ok, code: payload.code, exit: payload.exit };
          },
          onError: (payload) => {
            appendError(payload.message);
            outcome = { ok: false, code: payload.code, exit: false };
          },
        },
        controller.signal,
      );
    } catch (error) {
      if (!controller.signal.aborted) {
        appendError(error instanceof Error ? error.message : String(error));
        outcome = { ok: false, code: 'stream', exit: false };
      }
    } finally {
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
    try {
      return await completeConsoleLine(line, pos);
    } catch {
      // Tab-completion is a convenience, not the command itself; a network
      // hiccup should leave the line alone, not surface as an error block.
      return { from: pos, items: [] };
    }
  }

  function clearScrollback(): void {
    scrollback.value = [];
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
