// The release notice a super administrator sees in the backoffice.
//
// Asked once per page load, and only by a super administrator: the endpoint
// refuses everybody else, and the software version of an instance is not
// something a delegated operator needs to be shown. The dialog and the
// dashboard read the same answer, so they cannot disagree about it.

import { ref } from 'vue';
import { adminApi, type UpdateStatus } from './api';
import { safeHref } from '@/chat/markdown';

const SKIPPED_KEY = 'obsidian-arc-admin-update-skipped';

/** This page load's answer, or null while it is pending or failed. */
export const updateStatus = ref<UpdateStatus | null>(null);

let asked: Promise<void> | null = null;

/**
 * Asks the server, once. A failure is not remembered: the next caller asks
 * again, because the usual reason is a visit that is not open yet, and that
 * is a reason to ask later rather than to show nothing for this page load.
 */
export function loadUpdateStatus(): Promise<void> {
  asked ??= adminApi.update()
    .then((status) => {
      updateStatus.value = status;
    })
    .catch(() => {
      asked = null;
    });
  return asked;
}

/**
 * Forgets the answer, so the next caller asks again. The shell never needs
 * this, since one page load is one visit; the tests need each case to start
 * from nothing, because the answer is held for the life of the module.
 */
export function resetUpdateStatus(): void {
  asked = null;
  updateStatus.value = null;
}

/** The version the administrator chose not to hear about, if any. */
export function skippedVersion(): string | null {
  try {
    return localStorage.getItem(SKIPPED_KEY);
  } catch {
    // Storage refused: the dialog simply comes back on the next page load.
    return null;
  }
}

export function skipVersion(version: string): void {
  try {
    localStorage.setItem(SKIPPED_KEY, version);
  } catch {
    // Same as reading: the dialog returns, which is the safe way to fail.
  }
}

/**
 * The release the dialog should offer right now, or null.
 *
 * Skipping is per version: once the next release is out, the dialog is
 * allowed to speak again. The dashboard line ignores skipping, since it is
 * the quiet, persistent form of the same fact.
 */
export function offeredRelease(status: UpdateStatus | null, skipped: string | null): UpdateStatus | null {
  if (!status?.update_available) return null;
  return status.latest === skipped ? null : status;
}

export function hasCloudflareNotice(status: UpdateStatus | null): boolean {
  return !!status?.notices.some((notice) => notice.kind === 'cloudflare_unclaimed');
}

/** The release page, if the address is one a link may point at. */
export function releaseLink(status: UpdateStatus): string | null {
  return safeHref(status.url);
}
