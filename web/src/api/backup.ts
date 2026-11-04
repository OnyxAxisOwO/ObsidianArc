// Taking an account's data out, and putting it back.
//
// The export is one JSON document holding preferences and every conversation.
// It is fetched rather than streamed because the browser has to hold it all
// anyway to save it as a file.

import { t } from '@/composables/useI18n';
import { ApiError, api } from './client';

interface BackupTurn {
  role: 'user' | 'assistant';
  content: string;
  reasoning?: string;
  error?: string;
  model_name?: string;
  created_at?: number;
  images?: number;
}

interface BackupThread {
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

interface ImportResult {
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
export function saveAsFile(name: string, contents: string, type = 'application/json'): void {
  const url = URL.createObjectURL(new Blob([contents], { type }));
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
 *
 * The limit is on the body that is sent, which is the compact encoding of the
 * document: that is what the server counts. A file saved with indentation is
 * larger than its body, and an old indented export can be several times as
 * large, so the file itself is capped at eight times the limit. That bounds
 * what reading it whole costs without refusing a file the server would take.
 */
export function pickJSONFile(maxBytes = 32 * 1024 * 1024): Promise<unknown | null> {
  return new Promise((resolve, reject) => {
    const input = document.createElement('input');
    input.type = 'file';
    input.accept = 'application/json,.json';

    // Browsers that support it fire cancel when the picker is dismissed and
    // no change at all; without this the promise would hang forever.
    input.addEventListener('cancel', () => resolve(null), { once: true });
    input.addEventListener('change', () => {
      const file = input.files?.[0];
      if (!file) {
        resolve(null);
        return;
      }
      if (file.size > maxBytes * 8) {
        reject(new ApiError(0, 'too_large', t('importFileTooLarge')));
        return;
      }

      const reader = new FileReader();
      reader.onerror = () => reject(new ApiError(0, 'unreadable', t('importFileUnreadable')));
      reader.onload = () => {
        let parsed: unknown;
        try {
          parsed = JSON.parse(String(reader.result));
        } catch {
          reject(new ApiError(0, 'malformed', t('importFileNotJSON')));
          return;
        }
        if (new Blob([JSON.stringify(parsed)]).size > maxBytes) {
          reject(new ApiError(0, 'too_large', t('importFileTooLarge')));
          return;
        }
        resolve(parsed);
      };
      reader.readAsText(file);
    });

    input.click();
  });
}
