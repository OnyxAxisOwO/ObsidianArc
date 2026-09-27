// The one place a network request is made.
//
// Everything the interface knows about the server goes through here, which is
// what makes "what does the client call" answerable by reading one directory,
// and what makes a single change enough when session handling or error shapes
// move.

import { t } from '@/composables/useI18n';

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
  if (options.body !== undefined) {
    init.headers = { ...init.headers, 'Content-Type': 'application/json' };
    init.body = JSON.stringify(options.body);
  }

  let response: Response;
  try {
    response = await fetch(path, init);
  } catch (error) {
    if (options.signal?.aborted) throw error;
    throw new ApiError(0, 'network', networkMessage(error));
  }

  if (response.status === 204) return undefined as T;

  const text = await response.text();
  let payload: unknown = null;
  if (text) {
    try {
      payload = JSON.parse(text);
    } catch {
      payload = null;
    }
  }

  if (!response.ok) {
    const body = (payload ?? {}) as ErrorBody;
    const { code, message, ...details } = body.error ?? {};
    // The backoffice's visit ran out while a page was open. Every page would
    // otherwise draw its own error; the shell listens for this instead and
    // puts the place to type a code where the page was.
    if (code === 'two_factor_backoffice_verify' && typeof window !== 'undefined') {
      window.dispatchEvent(new Event(BACKOFFICE_LOCKED));
    }
    throw new ApiError(
      response.status,
      code ?? 'error',
      message ?? `Request failed with HTTP ${response.status}.`,
      details,
    );
  }

  return payload as T;
}

function networkMessage(error: unknown): string {
  const detail = error instanceof Error ? error.message : String(error);
  return `Could not reach the server: ${detail}`;
}

export const api = {
  get: <T>(path: string, options?: RequestOptions) => request<T>('GET', path, options ?? {}),
  post: <T>(path: string, body?: unknown, options?: RequestOptions) =>
    request<T>('POST', path, { ...options, body }),
  patch: <T>(path: string, body?: unknown, options?: RequestOptions) =>
    request<T>('PATCH', path, { ...options, body }),
  put: <T>(path: string, body?: unknown, options?: RequestOptions) =>
    request<T>('PUT', path, { ...options, body }),
  delete: <T>(path: string, options?: RequestOptions) => request<T>('DELETE', path, options ?? {}),
};

export interface Health {
  status: string;
  version?: string;
  uptime_sec?: number;
}

export function health(): Promise<Health> {
  return api.get<Health>('/api/health');
}
