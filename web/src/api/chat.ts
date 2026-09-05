// The chat API: conversations, attachments, and the streamed turn.
//
// The stream is read with fetch and a ReadableStream rather than EventSource,
// because the request is a POST carrying the turn — and because aborting a
// fetch is what cancels the whole chain: the browser closes the connection,
// the server's request context is cancelled, and the provider stops
// generating. Pressing Stop is one `AbortController.abort()`.

import { t } from '../i18n';
import { ApiError, api } from './client';

export interface Conversation {
  id: string;
  title: string;
  model_id: string;
  pinned: boolean;
  message_count: number;
  created_at: number;
  updated_at: number;
}

export interface MessageStats {
  ms: number;
  first_token_ms?: number;
  streamed: boolean;
  input_tokens?: number;
  output_tokens?: number;
  reasoning_tokens?: number;
  tps?: number;
}

export interface AttachmentRef {
  id: string;
  mime: string;
  width: number;
  height: number;
  size: number;
}

export interface Message {
  id: string;
  seq: number;
  role: 'user' | 'assistant';
  content: string;
  reasoning?: string;
  error?: string;
  model_id?: string;
  model_name?: string;
  stats?: MessageStats;
  attachments?: AttachmentRef[];
  created_at: number;
}

export function listConversations(): Promise<{ conversations: Conversation[] }> {
  return api.get<{ conversations: Conversation[] }>('/api/conversations');
}

export function getConversation(id: string): Promise<{ conversation: Conversation; messages: Message[] }> {
  return api.get<{ conversation: Conversation; messages: Message[] }>(`/api/conversations/${id}`);
}

export function renameConversation(id: string, title: string): Promise<{ conversation: Conversation }> {
  return api.patch<{ conversation: Conversation }>(`/api/conversations/${id}`, { title });
}

export function deleteConversation(id: string): Promise<void> {
  return api.delete<void>(`/api/conversations/${id}`);
}

export function deleteAllConversations(): Promise<{ deleted: number }> {
  return api.delete<{ deleted: number }>('/api/conversations');
}

export function uploadAttachment(input: {
  mime: string;
  data: string;
  width: number;
  height: number;
}): Promise<{ attachment: AttachmentRef }> {
  return api.post<{ attachment: AttachmentRef }>('/api/attachments', input);
}

export function attachmentURL(id: string): string {
  return `/api/attachments/${id}`;
}

// --- the streamed turn ---------------------------------------------------------

export interface TurnRequest {
  conversation_id?: string;
  model_id: string;
  content?: string;
  attachment_ids?: string[];
  reasoning?: { enabled: boolean; effort: string };
  truncate_from_message_id?: string;
}

export interface TurnHandlers {
  onStart?(payload: {
    conversation_id: string;
    title: string;
    user_message_id?: string;
    model_id: string;
    model_name: string;
  }): void;
  onDelta?(text: string): void;
  onReasoning?(text: string): void;
  onUsage?(usage: { input_tokens: number; output_tokens: number; reasoning_tokens: number }): void;
  onDone?(payload: {
    message_id: string;
    stats?: MessageStats;
    stopped: boolean;
    streamed: boolean;
    stream_fallback?: string;
  }): void;
  onError?(payload: { code: string; message: string; message_id?: string }): void;
}

/**
 * Runs one turn. Resolves when the stream ends; rejects only if the request
 * could not be made at all. A failure the server reported arrives through
 * onError, because by then the response has already started and there is no
 * status code left to carry it.
 */
export async function sendTurn(
  request: TurnRequest,
  handlers: TurnHandlers,
  signal: AbortSignal,
): Promise<void> {
  const response = await fetch('/api/chat', {
    method: 'POST',
    credentials: 'same-origin',
    headers: { 'Content-Type': 'application/json', Accept: 'text/event-stream' },
    body: JSON.stringify(request),
    signal,
  });

  if (!response.ok) {
    // Everything checkable — the model, the permission, the quota — is
    // checked before the stream opens, so a refusal is still an ordinary
    // JSON error with a real status.
    const text = await response.text();
    let code = 'error';
    let message = `Request failed with HTTP ${response.status}.`;
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
    throw new ApiError(response.status, code, message, details);
  }

  if (!response.body) throw new ApiError(0, 'stream', t('streamMissing'));

  const reader = response.body.getReader();
  const decoder = new TextDecoder();
  let buffer = '';

  for (;;) {
    const { value, done } = await reader.read();
    if (done) break;
    buffer += decoder.decode(value, { stream: true });

    // Events are separated by a blank line. `\r\n\r\n` as well as `\n\n`,
    // because some proxies rewrite line endings and a parser that only knows
    // about `\n` sees one enormous event that never ends.
    let boundary = /\r?\n\r?\n/.exec(buffer);
    while (boundary) {
      const frame = buffer.slice(0, boundary.index);
      buffer = buffer.slice(boundary.index + boundary[0].length);
      boundary = /\r?\n\r?\n/.exec(buffer);
      dispatch(frame, handlers);
    }
  }
}

function dispatch(frame: string, handlers: TurnHandlers): void {
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
    return;
  }

  switch (event) {
    case 'start':
      handlers.onStart?.(payload as Parameters<NonNullable<TurnHandlers['onStart']>>[0]);
      break;
    case 'delta':
      handlers.onDelta?.((payload as { text: string }).text);
      break;
    case 'reasoning':
      handlers.onReasoning?.((payload as { text: string }).text);
      break;
    case 'usage':
      handlers.onUsage?.(payload as Parameters<NonNullable<TurnHandlers['onUsage']>>[0]);
      break;
    case 'done':
      handlers.onDone?.(payload as Parameters<NonNullable<TurnHandlers['onDone']>>[0]);
      break;
    case 'error':
      handlers.onError?.(payload as Parameters<NonNullable<TurnHandlers['onError']>>[0]);
      break;
  }
}
