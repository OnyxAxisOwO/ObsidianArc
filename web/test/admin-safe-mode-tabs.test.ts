// Safe Mode is remembered in localStorage, which every open tab shares. A tab
// that was open before another tab changed a category must take that change
// on, or its next write puts the old value back.

import { afterEach, beforeEach, describe, expect, it } from 'vitest';
import { nextTick } from 'vue';
import {
  safeModeCategories,
  safeModeEnabled,
  STORAGE_KEY,
  toggleSafeMode,
} from '@/admin/safeMode';

function storedAs(enabled: boolean, users: boolean): string {
  return JSON.stringify({
    enabled,
    categories: { users, providers: true, credentials: true, billing: true, logs: true },
  });
}

describe('safe mode across tabs', () => {
  beforeEach(() => {
    localStorage.clear();
    safeModeEnabled.value = false;
    for (const key of ['users', 'providers', 'credentials', 'billing', 'logs'] as const) {
      safeModeCategories.value[key] = true;
    }
  });

  afterEach(() => {
    localStorage.clear();
    safeModeEnabled.value = false;
  });

  it('takes on a category change made in another tab instead of writing the stale value back', async () => {
    const otherTab = storedAs(true, false);
    localStorage.setItem(STORAGE_KEY, otherTab);
    window.dispatchEvent(new StorageEvent('storage', { key: STORAGE_KEY, newValue: otherTab, storageArea: localStorage }));
    await nextTick();
    expect(safeModeCategories.value.users).toBe(false);

    toggleSafeMode(false);
    await nextTick();

    const stored = JSON.parse(localStorage.getItem(STORAGE_KEY)!);
    expect(stored.enabled).toBe(false);
    expect(stored.categories.users).toBe(false);
  });

  it('ignores storage events for other keys', async () => {
    window.dispatchEvent(new StorageEvent('storage', {
      key: 'something-else',
      newValue: storedAs(true, false),
      storageArea: localStorage,
    }));
    await nextTick();

    expect(safeModeCategories.value.users).toBe(true);
    expect(safeModeEnabled.value).toBe(false);
  });
});
