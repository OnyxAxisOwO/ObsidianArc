// The server-sent-event reader every streamed request shares.
//
// fetch + ReadableStream rather than EventSource, because each stream here is
// a POST and aborting the fetch is how it is stopped. That leaves the framing
// to us, and it was written out once per stream until the copies began to
// differ in how they joined a multi-line data field. One copy now; what each
// stream does with an event stays with the stream.

import { ApiError, noteSessionEnded } from '@/api/client';
import { t } from '@/composables/useI18n';

export interface SSEEvent {
  /** The frame's `event:` field, or the caller's default when it had none. */
  event: string;
  /** Every `data:` line of the frame, joined with newlines as the format says. */
  data: string;
}

/** One frame, or null when it carried no data (a keepalive comment). */
export function parseSSEFrame(frame: string, defaultEvent = ''): SSEEvent | null {
  let event = defaultEvent;
  const data: string[] = [];
  for (const line of frame.split(/\r?\n/)) {
    if (line.startsWith('event:')) event = line.slice(6).trim();
    // One leading space is the separator the format allows after the colon;
    // anything past it belongs to the value.
    else if (line.startsWith('data:')) data.push(line.slice(line.startsWith('data: ') ? 6 : 5));
  }
  return data.length ? { event, data: data.join('\n') } : null;
}

/**
 * Reads the body to its end, handing each complete frame to onEvent. A frame
 * still open when the body ends is dropped: it never finished saying anything.
 *
 * Rejects when the read fails or onEvent throws. Either way the body is
 * cancelled first, because left open the server goes on producing into a
 * stream nobody is reading — a generation that keeps billing.
 */
export async function readSSE(
  body: ReadableStream<Uint8Array>,
  onEvent: (event: SSEEvent) => void,
  defaultEvent = '',
): Promise<void> {
  const reader = body.getReader();
  const decoder = new TextDecoder();
  let buffer = '';
  try {
    for (;;) {
      const { value, done } = await reader.read();
      if (done) break;
      buffer += decoder.decode(value, { stream: true });

      // Events are separated by a blank line. `\r\n\r\n` as well as `\n\n`,
      // because some proxies rewrite line endings and a parser that only knows
      // about `\n` sees one enormous event that never ends.
      let boundary = /\r?\n\r?\n/.exec(buffer);
      while (boundary) {
        const frame = parseSSEFrame(buffer.slice(0, boundary.index), defaultEvent);
        buffer = buffer.slice(boundary.index + boundary[0].length);
        boundary = /\r?\n\r?\n/.exec(buffer);
        if (frame) onEvent(frame);
      }
    }
  } catch (failure) {
    await reader.cancel().catch(() => undefined);
    throw failure;
  }
}

/**
 * The refusal a stream got before it opened. Everything checkable is checked
 * before the first event, so a refusal is an ordinary JSON error with a real
 * status, read the way the JSON client reads one.
 */
export async function streamRefusal(response: Response): Promise<ApiError> {
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
  // A stream is a request like any other: a session that ended while the page
  // was open must sign this tab out too, not only the JSON calls.
  noteSessionEnded(code);
  return new ApiError(response.status, code, message, details);
}
