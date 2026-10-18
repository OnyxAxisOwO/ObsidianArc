// The one place a network request is made.
//
// Everything the interface knows about the server goes through here, which is
// what makes "what does the client call" answerable by reading one directory,
// and what makes a single change enough when session handling or error shapes
// move.

import { t } from '@/composables/useI18n';
import { SESSION_ENDED } from './session-ended';

export class ApiError extends Error {
  readonly status: number;
  readonly code: string;
  readonly details: Record<string, unknown>;

  constructor(status: number, code: string, message: string, details: Record<string, unknown> = {}) {
    super(code === 'admin_permission_denied' ? t('permissionDeniedTitle') + ' · ' + t('permissionDeniedHint') : message);
    this.name = 'ApiError';
    this.status = status;
    this.code = code;
    this.details = details;
  }

  get isAuth(): boolean {
    return this.status === 401;
  }
}

/** Dispatched on window when the server turns a request away because the
 *  backoffice wants a code for this visit. */
export const BACKOFFICE_LOCKED = 'oa-backoffice-locked';

interface ErrorBody {
  error?: { code?: string; message?: string } & Record<string, unknown>;
}

export interface RequestOptions {
  signal?: AbortSignal;
  // Sent as JSON. Undefined means no body at all, which is what a GET wants.
  body?: unknown;
  // Sent as a multipart form, for the one kind of request that carries a file.
  // The browser writes the Content-Type, boundary included.
  form?: FormData;
}

async function request<T>(method: string, path: string, options: RequestOptions = {}): Promise<T> {
  const init: RequestInit = {
    method,
    // The session cookie is HttpOnly, so the browser attaches it; nothing in
    // this file ever sees or stores a credential.
    credentials: 'same-origin',
    headers: { Accept: 'application/json' },
  };
  if (options.signal) init.signal = options.signal;
  if (options.form) {
    init.body = options.form;
  } else if (options.body !== undefined) {
    init.headers = { ...init.headers, 'Content-Type': 'application/json' };
    init.body = JSON.stringify(options.body);
  }

  const response = await fetchApi(path, init);

  if (response.status === 204) return undefined as T;

  if (!response.ok) {
    const { code, message, details } = await readOrLost(() => readErrorBody(response), init.signal);
    noteBackofficeRefusal(code);
    noteSessionEnded(code);
    throw new ApiError(
      response.status,
      code ?? 'error',
      message ?? t('failed'),
      details,
    );
  }

  const text = await readOrLost(() => response.text(), init.signal);
  return parseJSON(text) as T;
}

// The body can break off after the headers arrived, which is the same lost
// connection fetchApi words for the reader, not the engine's TypeError.
async function readOrLost<R>(read: () => Promise<R>, signal: AbortSignal | null | undefined): Promise<R> {
  try {
    return await read();
  } catch (error) {
    if (signal?.aborted) throw error;
    throw new ApiError(0, 'network', t('connectionFailed'), { detail: networkDetail(error) });
  }
}

function parseJSON(text: string): unknown {
  if (!text) return null;
  try {
    return JSON.parse(text);
  } catch {
    return null;
  }
}

interface ErrorBodyParts {
  code?: string | undefined;
  message?: string | undefined;
  details: Record<string, unknown>;
}

/**
 * The parts of a refusal's `{"error": {...}}` body. No defaults and no
 * notices: each caller words a missing code its own way, and decides which of
 * the refusal notices it owes. A non-JSON body (a proxy's error page) reads as
 * nothing at all. Rejects only when the body itself cannot be read.
 */
export async function readErrorBody(response: Response): Promise<ErrorBodyParts> {
  const body = parseJSON(await response.text()) as ErrorBody | null;
  const error = body?.error;
  if (typeof error !== 'object' || error === null) return { details: {} };
  const { code, message, ...details } = error;
  return { code, message, details };
}

/**
 * The backoffice's visit ran out while a page was open. Every page would
 * otherwise draw its own error; the shell listens for this instead and puts
 * the place to type a code where the page was.
 *
 * Exported for the streams that call fetch directly (they never reach
 * `request`), which would otherwise leave an operator with a refusal and no
 * way to unlock.
 */
export function noteBackofficeRefusal(code: string | undefined): void {
  if (code === 'two_factor_backoffice_verify' && typeof window !== 'undefined') {
    window.dispatchEvent(new Event(BACKOFFICE_LOCKED));
  }
}

/**
 * The server answers a request that carries no live session with this code.
 * The other 401s — a wrong password, a second factor still due — have codes of
 * their own, and must not sign the tab out.
 */
export function noteSessionEnded(code: string | undefined): void {
  if (code === 'session_required' && typeof window !== 'undefined') {
    window.dispatchEvent(new Event(SESSION_ENDED));
  }
}

/**
 * fetch for every call this client makes, including the streamed ones that read
 * their own body. A connection that fails before any response arrives becomes
 * the same ApiError(0, 'network') a plain request gets, so a reader sees the
 * translated line rather than the engine's "Failed to fetch". An abort is the
 * caller's own doing and passes through unchanged.
 */
export async function fetchApi(path: string, init: RequestInit): Promise<Response> {
  try {
    return await fetch(path, init);
  } catch (error) {
    if (init.signal?.aborted) throw error;
    // The browser's own wording is engine text, not a sentence for a reader,
    // so it travels in details for anyone debugging.
    throw new ApiError(0, 'network', t('connectionFailed'), { detail: networkDetail(error) });
  }
}

function networkDetail(error: unknown): string {
  return error instanceof Error ? error.message : String(error);
}

export const api = {
  // No `?? {}` here: request's own default already stands in for a missing options.
  get: <T>(path: string, options?: RequestOptions) => request<T>('GET', path, options),
  post: <T>(path: string, body?: unknown, options?: RequestOptions) =>
    request<T>('POST', path, { ...options, body }),
  patch: <T>(path: string, body?: unknown, options?: RequestOptions) =>
    request<T>('PATCH', path, { ...options, body }),
  put: <T>(path: string, body?: unknown, options?: RequestOptions) =>
    request<T>('PUT', path, { ...options, body }),
  delete: <T>(path: string, options?: RequestOptions) => request<T>('DELETE', path, options),
  /** POST a file, as a form. */
  upload: <T>(path: string, form: FormData, options?: RequestOptions) =>
    request<T>('POST', path, { ...options, form }),
};

export interface Health {
  status: string;
  version?: string;
  uptime_sec?: number;
}

export function health(): Promise<Health> {
  return api.get<Health>('/api/health');
}
