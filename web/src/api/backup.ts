// Taking an account's data out, and putting it back.
//
// The export is one JSON document holding preferences and every conversation.
// It is fetched rather than streamed because the browser has to hold it all
// anyway to save it as a file.

import { ApiError, api } from './client';

export interface BackupTurn {
  role: 'user' | 'assistant';
  content: string;
  reasoning?: string;
  error?: string;
  model_name?: string;
  created_at?: number;
  images?: number;
}

export interface BackupThread {
  title: string;
  pinned?: boolean;
  created_at?: number;
  messages: BackupTurn[];
}

export interface BackupDocument {
  obsidian_arc_export: number;
  exported_at: number;
  username?: string;
  preferences?: unknown;
  conversations: BackupThread[];
}

export interface ImportResult {
  conversations: number;
  messages: number;
  preferences: boolean;
  skipped: number;
}

export function exportAccount(): Promise<BackupDocument> {
  return api.get<BackupDocument>('/api/account/export');
}

export function importAccount(document: unknown): Promise<ImportResult> {
  return api.post<ImportResult>('/api/account/import', document);
}

/**
 * Hands the browser a file to save.
 *
 * An object URL rather than a data: one because an export can be megabytes,
 * and it is revoked on the next frame — long enough for the click to have
 * been dispatched, short enough not to pin the whole document in memory.
 */
export function saveAsFile(name: string, contents: string): void {
  const url = URL.createObjectURL(new Blob([contents], { type: 'application/json' }));
  const link = document.createElement('a');
  link.href = url;
  link.download = name;
  link.click();
  requestAnimationFrame(() => URL.revokeObjectURL(url));
}

/**
 * Asks for one JSON file and parses it.
 *
 * Resolves with null when the picker is dismissed, which is not an error and
 * should not be reported as one.
 */
export function pickJSONFile(maxBytes = 32 * 1024 * 1024): Promise<unknown | null> {
  return new Promise((resolve, reject) => {
    const input = document.createElement('input');
    input.type = 'file';
    input.accept = 'application/json,.json';

    input.addEventListener('change', () => {
      const file = input.files?.[0];
      if (!file) {
        resolve(null);
        return;
      }
      if (file.size > maxBytes) {
        reject(new ApiError(0, 'too_large', 'That file is too large to import.'));
        return;
      }

      const reader = new FileReader();
      reader.onerror = () => reject(new ApiError(0, 'unreadable', 'That file could not be read.'));
      reader.onload = () => {
        try {
          resolve(JSON.parse(String(reader.result)));
        } catch {
          reject(new ApiError(0, 'malformed', 'That file is not valid JSON.'));
        }
      };
      reader.readAsText(file);
    });

    input.click();
  });
}
