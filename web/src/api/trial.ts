// The front door's sample conversation.
//
// Nothing is stored on either side: the whole exchange is sent with every
// turn, which is what lets the server count the turns itself rather than
// trusting a number from here. There is no conversation id to keep and no
// account to keep it against.

import { ApiError } from './client';
import { t } from '../i18n';

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
export async function streamTrial(
  messages: TrialMessage[],
  onDelta: (text: string) => void,
): Promise<void> {
  const response = await fetch('/api/trial/chat', {
    method: 'POST',
    headers: { 'Content-Type': 'application/json', Accept: 'text/event-stream' },
    body: JSON.stringify({ messages }),
  });

  if (!response.ok) {
    const body = (await response.json().catch(() => null)) as
      | { error?: { code?: string; message?: string } }
      | null;
    throw new ApiError(
      response.status,
      body?.error?.code ?? 'trial',
      body?.error?.message ?? t('trialFailed'),
    );
  }
  if (!response.body) throw new ApiError(0, 'stream', t('streamMissing'));

  const reader = response.body.getReader();
  const decoder = new TextDecoder();
  let buffer = '';

  for (;;) {
    const { done, value } = await reader.read();
    if (done) break;
    buffer += decoder.decode(value, { stream: true });

    // SSE frames are separated by a blank line; anything after the last one
    // is a partial frame and waits for the next chunk.
    let boundary = buffer.indexOf('\n\n');
    while (boundary !== -1) {
      handleFrame(buffer.slice(0, boundary), onDelta);
      buffer = buffer.slice(boundary + 2);
      boundary = buffer.indexOf('\n\n');
    }
  }
}

function handleFrame(frame: string, onDelta: (text: string) => void): void {
  let event = 'message';
  const data: string[] = [];

  for (const line of frame.split('\n')) {
    if (line.startsWith('event:')) event = line.slice(6).trim();
    else if (line.startsWith('data:')) data.push(line.slice(5).trimStart());
  }
  if (!data.length) return;

  let payload: { text?: string; message?: string } | null = null;
  try {
    payload = JSON.parse(data.join('\n')) as { text?: string; message?: string };
  } catch {
    return;
  }

  if (event === 'delta' && payload?.text) onDelta(payload.text);
  else if (event === 'error') throw new ApiError(502, 'upstream', payload?.message ?? t('trialFailed'));
}
