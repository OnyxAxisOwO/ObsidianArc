// The front door's sample conversation.
//
// Nothing is stored on either side: the whole exchange is sent with every
// turn, which is what lets the server count the turns itself rather than
// trusting a number from here. There is no conversation id to keep and no
// account to keep it against.

import { ApiError, fetchApi } from './client';
import { readSSE, type SSEEvent } from './sse';
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

  // Cast rather than annotated: it is set inside the callback, which
  // TypeScript's narrowing does not follow, so `null` would stick.
  let result = null as TrialResult | null;
  // An error frame throws out of the read, and the reader cancels the body
  // so the server stops producing an answer nobody is reading.
  await readSSE(response.body, (event) => {
    const finished = handleFrame(event, onDelta);
    if (finished) result = finished;
  }, 'message');

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
function handleFrame({ event, data }: SSEEvent, onDelta: (text: string) => void): TrialResult | null {
  let payload: Frame | null = null;
  try {
    payload = JSON.parse(data) as Frame;
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
