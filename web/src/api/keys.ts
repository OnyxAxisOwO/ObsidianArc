// API keys, as the interface sees them.
//
// The token is in exactly one response — the one that creates the key — and
// this file is careful never to keep it: it is handed to the caller and
// forgotten here, because the server cannot reissue it and a copy sitting in
// a module variable would be a copy nobody asked for.

import { api } from './client';

export interface ApiKey {
  id: string;
  /** The opening characters, so two keys can be told apart in a list. */
  prefix: string;
  name: string;
  /** Epoch millis; zero means it never expires. */
  expires_at: number;
  last_used_at: number;
  created_at: number;
  updated_at: number;
}

export interface KeyList {
  keys: ApiKey[];
  /** Whether a key created now would work: the instance switch and the group grant. */
  enabled: boolean;
  max: number;
}

export function listKeys(): Promise<KeyList> {
  return api.get<KeyList>('/api/keys');
}

/**
 * Creates a key.
 *
 * The token in the result is the only copy that will ever exist. Show it
 * immediately; there is no endpoint that can produce it again.
 */
export function createKey(name: string, expiresAt: number): Promise<{ key: ApiKey; token: string }> {
  return api.post<{ key: ApiKey; token: string }>('/api/keys', { name, expires_at: expiresAt });
}

export function updateKey(id: string, changes: { name?: string; expires_at?: number }): Promise<ApiKey> {
  return api.patch<ApiKey>(`/api/keys/${id}`, changes);
}

export function deleteKey(id: string): Promise<void> {
  return api.delete<void>(`/api/keys/${id}`);
}
