// The terminal's HTTP client: CONTRACT.md #4.
//
// Three endpoints, one of which streams. `exec` is deliberately the same
// shape as `sendTurn` in `api/chat.ts` — fetch + ReadableStream, not
// EventSource — because a POST is what carries the command line, and
// aborting a fetch is the only cancellation mechanism: there is no stop
// endpoint, so `Ctrl-C` in the pane is one `AbortController.abort()`.

import { ApiError, api, fetchApi, noteSessionEnded } from '@/api/client';
import { currentLanguage, t } from '@/composables/useI18n';

export interface ConsoleYou {
  username: string;
  role: 'super_admin' | 'admin' | 'user';
  permissions: string[];
}

export interface ConsoleSSHInfo {
  enabled: boolean;
  addr: string;
  fingerprint: string;
}

/** One flag a command accepts, as the spec describes it for `help` and completion. */
export interface ConsoleCommandFlag {
  name: string;
  hint: string;
  /** Absent for a boolean flag like `--yes`; present names the value's placeholder, e.g. "TEXT". */
  value?: string | undefined;
}

/** One positional argument a command accepts. */
export interface ConsoleCommandArg {
  name: string;
  hint: string;
  required: boolean;
}

/** One command, as the spec describes it — everything the client needs to render `help` and complete locally without a round trip per keystroke. */
export interface ConsoleCommandSpec {
  name: string;
  group: string;
  summary: string;
  usage: string;
  /** Copied from the route table; "" means any administrator. Never re-derived client-side — the server checks it again on every call. */
  permission: string;
  destructive: boolean;
  flags: ConsoleCommandFlag[];
  args: ConsoleCommandArg[];
  examples: string[];
  endpoints: string[];
}

/** `GET /api/console/spec` — computed for the calling actor; a command they may not run is simply absent, not disabled. */
export interface ConsoleSpec {
  version: string;
  you: ConsoleYou;
  ssh: ConsoleSSHInfo;
  /** The first block the terminal prints, before any prompt. */
  banner: string;
  commands: ConsoleCommandSpec[];
}

export function fetchConsoleSpec(signal?: AbortSignal): Promise<ConsoleSpec> {
  const query = `?lang=${encodeURIComponent(currentLanguage())}`;
  return api.get<ConsoleSpec>(`/api/console/spec${query}`, signal ? { signal } : {});
}

export interface ConsoleCompletionItem {
  value: string;
  label: string;
  hint: string;
}

/** `POST /api/console/complete` response. `from` is a byte offset into the line where the replacement starts, matching how the server sliced it. */
export interface ConsoleCompletion {
  from: number;
  items: ConsoleCompletionItem[];
}

// The hints should match the language the tab's output comes back in, so a tab
// that ran `lang` passes its language; otherwise the UI language applies.
export function completeConsoleLine(
  line: string,
  pos: number,
  lang?: string,
  signal?: AbortSignal,
): Promise<ConsoleCompletion> {
  return api.post<ConsoleCompletion>(
    '/api/console/complete',
    { line, pos, lang: lang ?? currentLanguage() },
    signal ? { signal } : {},
  );
}

/** `POST /api/console/exec` request body. `lang` defaults to `currentLanguage()` in `execConsoleLine`, the same as every other admin request; a tab that has run `lang` passes the value back so it sticks. */
export interface ConsoleExecRequest {
  line: string;
  /** Terminal columns, for the server's own table layout (`render.Table`). */
  cols: number;
  json: boolean;
  /**
   * The language the last `lang` command left this tab on, carried back from
   * the done frame. Absent until a command sets one; then the UI language is
   * the default.
   */
  lang?: string;
}

/** `event: done` payload — the command's own verdict, not a transport-level failure. */
export interface ConsoleExecDone {
  ok: boolean;
  code: string;
  exit: boolean;
  elapsed_ms: number;
  /**
   * What `format` and `lang` left the session set to.
   *
   * SSH keeps one session per connection and simply mutates it. A browser
   * has no connection to keep anything on — every exec builds a fresh
   * session from the request — so the server hands these back and the tab
   * carries them into its next request. That is what makes `format json`
   * mean "for the rest of this session" here as well.
   */
  json: boolean;
  lang: string;
}

/** `event: error` payload — an engine failure (bad permission, a line that could not be parsed), never a command's own error output. A command's own failure is text inside `onOut`, rendered by `render.go` as `error: <message>`. */
export interface ConsoleExecError {
  code: string;
  message: string;
}

export interface ConsoleExecHandlers {
  /** One `event: out` chunk. ANSI is allowed and arrives split at arbitrary byte boundaries — feed it through `ansi.ts`, not this module. */
  onOut?(text: string): void;
  onDone?(payload: ConsoleExecDone): void;
  onError?(payload: ConsoleExecError): void;
}

/**
 * Runs one command line. Resolves when the stream ends, whether that is a
 * `done` event, an `error` event, or the caller aborting `signal` — the
 * request could not be made at all is the only case that rejects, mirroring
 * `sendTurn`'s contract in `api/chat.ts`.
 */
export async function execConsoleLine(
  request: ConsoleExecRequest,
  handlers: ConsoleExecHandlers,
  signal: AbortSignal,
): Promise<void> {
  const response = await fetchApi('/api/console/exec', {
    method: 'POST',
    credentials: 'same-origin',
    headers: { 'Content-Type': 'application/json', Accept: 'text/event-stream' },
    body: JSON.stringify({ ...request, lang: request.lang ?? currentLanguage() }),
    signal,
  });

  if (!response.ok) {
    // Everything checkable before the stream opens — permission, a malformed
    // line — is checked before it opens, so a refusal here is still an
    // ordinary JSON error with a real status, same as `sendTurn`.
    const text = await response.text();
    let code = 'error';
    let message = t('failed');
    let details: Record<string, unknown> = {};
    try {
      const body = JSON.parse(text) as { error?: { code?: string; message?: string } & Record<string, unknown> };
      if (body.error) {
        const { code: bodyCode, message: bodyMessage, ...rest } = body.error;
        code = bodyCode ?? code;
        message = bodyMessage ?? message;
        details = rest;
      }
    } catch {
      // A non-JSON body (a proxy's error page) leaves the defaults.
    }
    // The terminal is a stream too; a console left open when the session
    // ends has to sign out with every other page, not sit on a dead prompt.
    noteSessionEnded(code);
    throw new ApiError(response.status, code, message, details);
  }

  if (!response.body) throw new ApiError(0, 'stream', t('streamMissing'));

  const reader = response.body.getReader();
  const decoder = new TextDecoder();
  let buffer = '';

  try {
    for (;;) {
      const { value, done } = await reader.read();
      if (done) break;
      buffer += decoder.decode(value, { stream: true });

      // `\r\n\r\n` as well as `\n\n` — see api/chat.ts for why: a proxy that
      // rewrites line endings must not turn this into one event that never
      // closes.
      let boundary = /\r?\n\r?\n/.exec(buffer);
      while (boundary) {
        const frame = buffer.slice(0, boundary.index);
        buffer = buffer.slice(boundary.index + boundary[0].length);
        boundary = /\r?\n\r?\n/.exec(buffer);
        dispatch(frame, handlers);
      }
    }
  } catch (error) {
    // Stop is one `AbortController.abort()` and nothing else — there is no
    // stop endpoint. The aborted read is the expected shape of cancelling,
    // not a failure to surface through onError.
    if (signal.aborted) return;
    throw error;
  }
}

function dispatch(frame: string, handlers: ConsoleExecHandlers): void {
  let event = '';
  let data = '';

  for (const line of frame.split(/\r?\n/)) {
    if (line.startsWith('event:')) event = line.slice(6).trim();
    else if (line.startsWith('data:')) data += line.slice(5).trim();
  }
  if (!data) return;

  let payload: unknown;
  try {
    payload = JSON.parse(data);
  } catch {
    // A keepalive comment or a malformed event must not end the stream.
    return;
  }

  switch (event) {
    case 'out':
      handlers.onOut?.((payload as { text: string }).text);
      break;
    case 'done':
      handlers.onDone?.(payload as ConsoleExecDone);
      break;
    case 'error':
      handlers.onError?.(payload as ConsoleExecError);
      break;
  }
}
