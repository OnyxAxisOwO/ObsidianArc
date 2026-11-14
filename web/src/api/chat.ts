// The chat API: conversations, attachments, and the streamed turn.
//
// The stream is read with fetch and a ReadableStream rather than EventSource,
// because the request is a POST carrying the turn — and because aborting a
// fetch is what cancels the whole chain: the browser closes the connection,
// the server's request context is cancelled, and the provider stops
// generating. Pressing Stop is one `AbortController.abort()`.

import { t } from '@/composables/useI18n';
import { ApiError, api, fetchApi } from './client';
import { readSSE, streamRefusal, type SSEEvent } from './sse';

export interface Conversation {
  id: string;
  project_id?: string;
  title: string;
  model_id: string;
  pinned: boolean;
  archived?: boolean;
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
  /** The provider reported no usage; the counts were estimated from the text. */
  estimated?: boolean;
}

export interface AttachmentRef {
  id: string;
  mime: string;
  width: number;
  height: number;
  size: number;
  /**
   * The bytes are no longer held: the picture reached the model and the
   * server dropped it. Only the record that it was sent survives, so the
   * transcript draws a placeholder rather than a broken image.
   */
  discarded?: boolean;
}

export interface Message {
  id: string;
  seq: number;
  role: 'user' | 'assistant';
  content: string;
  reasoning?: string;
  error?: string;
  error_code?: string;
  model_id?: string;
  model_name?: string;
  stats?: MessageStats;
  attachments?: AttachmentRef[];
  created_at: number;
}

export interface ListConversationsParams {
  project_id?: string;
  archived?: boolean;
  limit?: number;
  offset?: number;
}

export function listConversations(params?: ListConversationsParams): Promise<{ conversations: Conversation[] }> {
  const query = new URLSearchParams();
  if (params?.project_id !== undefined) query.set('project_id', params.project_id);
  if (params?.archived !== undefined) query.set('archived', String(params.archived));
  if (params?.limit !== undefined) query.set('limit', String(params.limit));
  if (params?.offset !== undefined) query.set('offset', String(params.offset));
  const qs = query.toString();
  return api.get<{ conversations: Conversation[] }>(`/api/conversations${qs ? `?${qs}` : ''}`);
}

export function archiveConversation(id: string, archived = true): Promise<{ conversation: Conversation }> {
  return api.patch<{ conversation: Conversation }>(`/api/conversations/${id}`, { archived });
}

export function getConversation(id: string): Promise<{ conversation: Conversation; messages: Message[] }> {
  return api.get<{ conversation: Conversation; messages: Message[] }>(`/api/conversations/${id}`);
}

export function renameConversation(id: string, title: string): Promise<{ conversation: Conversation }> {
  return api.patch<{ conversation: Conversation }>(`/api/conversations/${id}`, { title });
}

/**
 * Files a conversation under one of the owner's projects, or with "" takes it
 * out of every project. The server answers 404 for a project that is not the
 * owner's or is gone, and leaves updated_at alone, so filing never reorders
 * the rail.
 */
export function moveConversation(id: string, projectID: string): Promise<{ conversation: Conversation }> {
  return api.patch<{ conversation: Conversation }>(`/api/conversations/${id}`, { project_id: projectID });
}

export function deleteConversation(id: string): Promise<void> {
  return api.delete<void>(`/api/conversations/${id}`);
}

export function updateMessage(
  conversationID: string,
  messageID: string,
  content: string,
): Promise<{ message: Message }> {
  return api.patch<{ message: Message }>(`/api/conversations/${conversationID}/messages/${messageID}`, { content });
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

interface TurnRequest {
  conversation_id?: string;
  model_id: string;
  content?: string;
  attachment_ids?: string[];
  reasoning?: { enabled: boolean; effort: string };
  truncate_from_message_id?: string;
  /** Search the public web for this turn before asking the model. */
  web_search?: boolean;
  turnstile?: string;
  /**
   * Which project a NEW conversation opens in. The server reads it only
   * while creating one — an existing thread keeps what it was opened with.
   */
  project_id?: string;
}

export interface TurnHandlers {
  onStart?(payload: {
    conversation_id: string;
    title: string;
    user_message_id?: string;
    model_id: string;
    model_name: string;
  }): void;
  /** The search happened before the first token; the answer has not started. */
  onSearch?(payload: { results: number; failed?: boolean; searching?: boolean }): void;
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
 * Runs one turn. Resolves when the stream ends. Rejects when the request could
 * not be made, when the server refuses it before the stream opens, or when the
 * stream breaks partway (an abort, a dropped connection, or a handler that
 * throws). A failure the server reports once the stream is open arrives through
 * onError, because by then there is no status code left to carry it.
 */
export async function sendTurn(
  request: TurnRequest,
  handlers: TurnHandlers,
  signal: AbortSignal,
): Promise<void> {
  const response = await fetchApi('/api/chat', {
    method: 'POST',
    credentials: 'same-origin',
    headers: { 'Content-Type': 'application/json', Accept: 'text/event-stream' },
    body: JSON.stringify(request),
    signal,
  });

  // Everything checkable — the model, the permission, the quota — is checked
  // before the stream opens, so a refusal still has a real status.
  if (!response.ok) throw await streamRefusal(response);
  if (!response.body) throw new ApiError(0, 'stream', t('streamMissing'));
  await readSSE(response.body, (event) => dispatch(event, handlers));
}

function dispatch({ event, data }: SSEEvent, handlers: TurnHandlers): void {
  let payload: unknown;
  try {
    payload = JSON.parse(data);
  } catch {
    return;
  }
  // Valid JSON that is not an object (`null`, a bare number) has no fields to
  // read; reading `.text` from it threw inside the read loop and ended the turn.
  if (typeof payload !== 'object' || payload === null) return;

  // The one place the frame's shape is asserted: the server writes each
  // payload from a struct, so its fields are taken as given, except the text
  // frames' string, which is checked below.
  const frame = { event, payload } as TurnFrame;
  switch (frame.event) {
    case 'start':
      handlers.onStart?.(frame.payload);
      break;
    case 'search':
      handlers.onSearch?.(frame.payload);
      break;
    // A text frame with no string in it would be joined into the answer as the
    // literal "undefined", so it is dropped rather than passed on.
    case 'delta': {
      const { text } = frame.payload;
      if (typeof text === 'string') handlers.onDelta?.(text);
      break;
    }
    case 'reasoning': {
      const { text } = frame.payload;
      if (typeof text === 'string') handlers.onReasoning?.(text);
      break;
    }
    case 'usage':
      handlers.onUsage?.(frame.payload);
      break;
    case 'done':
      handlers.onDone?.(frame.payload);
      break;
    case 'error':
      handlers.onError?.(frame.payload);
      break;
  }
}

type HandlerPayload<K extends keyof TurnHandlers> = Parameters<NonNullable<TurnHandlers[K]>>[0];

// Each event the turn stream sends, with the payload that event carries.
type TurnFrame =
  | { event: 'start'; payload: HandlerPayload<'onStart'> }
  | { event: 'search'; payload: HandlerPayload<'onSearch'> }
  | { event: 'delta'; payload: { text?: unknown } }
  | { event: 'reasoning'; payload: { text?: unknown } }
  | { event: 'usage'; payload: HandlerPayload<'onUsage'> }
  | { event: 'done'; payload: HandlerPayload<'onDone'> }
  | { event: 'error'; payload: HandlerPayload<'onError'> };
