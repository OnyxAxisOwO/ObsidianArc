// The front door's sample conversation.
//
// Nothing is stored on either side: the whole exchange is sent with every
// turn, which is what lets the server count the turns itself rather than
// trusting a number from here. There is no conversation id to keep and no
// account to keep it against.

import { ApiError, fetchApi } from './client';
import { t } from '@/composables/useI18n';

export interface TrialMessage {
  role: 'user' | 'assistant';
  content: string;
}

/**
 * Streams one trial answer, calling onDelta with each piece as it arrives.
 *
 * Read with fetch rather than EventSource for the same reason the signed-in
 * chat is: the request is a POST carrying the exchange, and aborting a fetch
 * is what cancels the whole chain.
 */
export interface TrialResult {
  /**
   * The server's signed count of turns used. Opaque here: it exists so the
   * limit is enforced against a number the server wrote rather than one this
   * page could edit. Send it back with the next question.
   */
  continuation: string;
  turnsLeft: number;
}

export async function streamTrial(
  messages: TrialMessage[],
  continuation: string,
  onDelta: (text: string) => void,
  signal?: AbortSignal,
): Promise<TrialResult> {
  const response = await fetchApi('/api/trial/chat', {
    method: 'POST',
    headers: { 'Content-Type': 'application/json', Accept: 'text/event-stream' },
    body: JSON.stringify({ messages, continuation }),
    signal: signal ?? null,
  });

  if (!response.ok) {
    const body = (await response.json().catch(() => null)) as
      | { error?: { code?: string; message?: string } }
      | null;
    const code = body?.error?.code ?? 'trial';
    throw new ApiError(
      response.status,
      code,
      trialErrorMessage(code, body?.error?.message ?? t('trialFailed')),
    );
  }
  if (!response.body) throw new ApiError(0, 'stream', t('streamMissing'));

  const reader = response.body.getReader();
  const decoder = new TextDecoder();
  let buffer = '';
  let result: TrialResult | null = null;

  try {
    for (;;) {
      const { done, value } = await reader.read();
      if (done) break;
      buffer += decoder.decode(value, { stream: true });

      // SSE frames are separated by a blank line; anything after the last one
      // is a partial frame and waits for the next chunk. CRLF is accepted
      // because a proxy may rewrite line endings, and a frame that never closes
      // is dropped without a word.
      let boundary = /\r?\n\r?\n/.exec(buffer);
      while (boundary) {
        const finished = handleFrame(buffer.slice(0, boundary.index), onDelta);
        if (finished) result = finished;
        buffer = buffer.slice(boundary.index + boundary[0].length);
        boundary = /\r?\n\r?\n/.exec(buffer);
      }
    }
  } catch (failure) {
    // An error frame or an abort ends the read early. The body is still open
    // at that point, and left open the server would go on producing an answer
    // nobody is reading.
    void reader.cancel().catch(() => {});
    throw failure;
  }

  // Only the done frame says how many turns the server counted. A stream
  // that ends without one (a proxy closing the body, or the server giving up
  // once the client has gone) must not read as an exhausted trial: that would
  // lock the visitor out with no error for a turn the server may not have
  // counted.
  if (!result) throw new ApiError(502, 'stream', t('trialFailed'));
  return result;
}

/**
 * The server's sentences are English. The codes listed below are translated;
 * any other code shows the server's sentence, the only text available for it.
 */
export function trialErrorMessage(code: string, serverMessage: string): string {
  switch (code) {
    case 'trial_exhausted':
      return t('trialExhausted');
    case 'trial_busy':
      return t('trialBusy');
    case 'trial_finished':
      return t('trialEndedSignUp');
    case 'trial_empty':
      return t('trialNothingToSend');
    case 'trial_too_long':
      return t('trialTooLong');
    case 'trial_message_too_long': {
      // The limit is only written in the server's sentence, so it is read from
      // there. Without a number in it the sentence is shown as it stands.
      const max = /\d+/.exec(serverMessage)?.[0];
      return max ? t('trialMessageTooLong', { max }) : serverMessage;
    }
    case 'trial_unavailable':
      return t('trialUnavailable');
    case 'upstream':
      return t('trialFailed');
    default:
      return serverMessage;
  }
}

interface Frame {
  text?: string;
  code?: string;
  message?: string;
  continuation?: string;
  turns_left?: number;
}

/** Returns the result when this frame was the closing one. */
function handleFrame(frame: string, onDelta: (text: string) => void): TrialResult | null {
  let event = 'message';
  const data: string[] = [];

  for (const line of frame.split(/\r?\n/)) {
    if (line.startsWith('event:')) event = line.slice(6).trim();
    else if (line.startsWith('data:')) data.push(line.slice(5).trimStart());
  }
  if (!data.length) return null;

  let payload: Frame | null = null;
  try {
    payload = JSON.parse(data.join('\n')) as Frame;
  } catch {
    return null;
  }

  if (event === 'delta' && payload?.text) onDelta(payload.text);
  else if (event === 'error') {
    const code = payload?.code ?? 'upstream';
    throw new ApiError(502, code, trialErrorMessage(code, payload?.message ?? t('trialFailed')));
  }
  else if (event === 'done') {
    return { continuation: payload?.continuation ?? '', turnsLeft: payload?.turns_left ?? 0 };
  }
  return null;
}
