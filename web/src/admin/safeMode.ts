// Administrator Safe Mode.
//
// When an administrator opens the backoffice (/admin), Safe Mode masks sensitive
// information (such as user details, provider endpoints and keys, quotas/billing,
// and IP/network logs) with asterisks (******) so that sharing a screen or
// recording a video cannot leak private instance data.
//
// The state is persisted in localStorage so an operator's chosen privacy posture
// is remembered across page navigations and refreshes.

import { computed, ref, watch } from 'vue';
import type { StringKey } from '@/composables/useI18n';

export type SafeCategory = 'users' | 'providers' | 'credentials' | 'billing' | 'logs';

export interface SafeCategoryInfo {
  key: SafeCategory;
  labelKey: StringKey;
  hintKey: StringKey;
}

// The categories are named again below, in the stored shape and its defaults,
// and those are Records the compiler checks for a missing key. This list is
// the one it could not check, and a category left out of it would still mask
// but have no switch, and be skipped by "select all" and by the count. The
// assertion after it makes that a type error too, at no cost to the bundle:
// the page that reads it rides in the first paint.
export const SAFE_CATEGORIES = [
  { key: 'users', labelKey: 'safeModeCategoryUsers', hintKey: 'safeModeCategoryUsersHint' },
  { key: 'providers', labelKey: 'safeModeCategoryProviders', hintKey: 'safeModeCategoryProvidersHint' },
  { key: 'credentials', labelKey: 'safeModeCategoryCredentials', hintKey: 'safeModeCategoryCredentialsHint' },
  { key: 'billing', labelKey: 'safeModeCategoryBilling', hintKey: 'safeModeCategoryBillingHint' },
  { key: 'logs', labelKey: 'safeModeCategoryLogs', hintKey: 'safeModeCategoryLogsHint' },
] as const satisfies readonly SafeCategoryInfo[];

type UnlistedCategory = Exclude<SafeCategory, (typeof SAFE_CATEGORIES)[number]['key']>;
// Fails to compile, naming the category, when one is missing from the list.
type CategoryListIsComplete = [UnlistedCategory] extends [never] ? true : UnlistedCategory;
true satisfies CategoryListIsComplete;

export const STORAGE_KEY = 'obsidian-arc-admin-safe-mode';
export const MASK_PLACEHOLDER = '******';

interface StoredSafeMode {
  enabled: boolean;
  categories: Record<SafeCategory, boolean>;
}

function parseStored(raw: string | null): StoredSafeMode {
  try {
    if (raw) {
      const parsed = JSON.parse(raw);
      if (typeof parsed === 'object' && parsed !== null) {
        return {
          enabled: Boolean(parsed.enabled),
          categories: {
            users: parsed.categories?.users !== false,
            providers: parsed.categories?.providers !== false,
            credentials: parsed.categories?.credentials !== false,
            billing: parsed.categories?.billing !== false,
            logs: parsed.categories?.logs !== false,
          },
        };
      }
    }
  } catch {
    // Unparseable: fall back to the defaults below
  }
  return {
    enabled: false,
    categories: {
      users: true,
      providers: true,
      credentials: true,
      billing: true,
      logs: true,
    },
  };
}

function loadStored(): StoredSafeMode {
  try {
    return parseStored(typeof localStorage !== 'undefined' ? localStorage.getItem(STORAGE_KEY) : null);
  } catch {
    // Local storage unavailable
    return parseStored(null);
  }
}

const initial = loadStored();
export const safeModeEnabled = ref(initial.enabled);
export const safeModeCategories = ref<Record<SafeCategory, boolean>>(initial.categories);

// The state belongs to the browser, not to one tab. Without this, a tab opened
// before a change made elsewhere writes its stale copy of the other settings
// the next time anything toggles, and silently undoes that change.
if (typeof window !== 'undefined') {
  window.addEventListener('storage', (event) => {
    if (event.key !== STORAGE_KEY || event.newValue === null) return;
    const next = parseStored(event.newValue);
    safeModeEnabled.value = next.enabled;
    safeModeCategories.value = next.categories;
  });
}

function persist(): void {
  try {
    if (typeof localStorage !== 'undefined') {
      localStorage.setItem(STORAGE_KEY, JSON.stringify({
        enabled: safeModeEnabled.value,
        categories: safeModeCategories.value,
      }));
    }
  } catch {
    // Ignore storage quota
  }
}

watch([safeModeEnabled, safeModeCategories], persist, { deep: true });

export function isMasked(category: SafeCategory): boolean {
  return safeModeEnabled.value && safeModeCategories.value[category] === true;
}

export function toggleSafeMode(force?: boolean): void {
  safeModeEnabled.value = force !== undefined ? force : !safeModeEnabled.value;
}

export function toggleCategory(category: SafeCategory, force?: boolean): void {
  safeModeCategories.value[category] = force !== undefined ? force : !safeModeCategories.value[category];
}

export function setAllCategories(value: boolean): void {
  for (const cat of SAFE_CATEGORIES) {
    safeModeCategories.value[cat.key] = value;
  }
}

export const activeMaskCount = computed(() => {
  if (!safeModeEnabled.value) return 0;
  return SAFE_CATEGORIES.filter((c) => safeModeCategories.value[c.key]).length;
});

/**
 * Replaces a sensitive value with asterisks when the given category is masked.
 */
export function mask(
  value: string | number | null | undefined,
  category: SafeCategory,
  placeholder: string = MASK_PLACEHOLDER,
): string {
  if (value === null || value === undefined || value === '') {
    return '';
  }
  if (!isMasked(category)) {
    return String(value);
  }
  return placeholder;
}

export function maskUser(value: string | number | null | undefined, placeholder: string = MASK_PLACEHOLDER): string {
  return mask(value, 'users', placeholder);
}

export function maskProvider(value: string | number | null | undefined, placeholder: string = MASK_PLACEHOLDER): string {
  return mask(value, 'providers', placeholder);
}

export function maskCredential(value: string | number | null | undefined, placeholder: string = MASK_PLACEHOLDER): string {
  return mask(value, 'credentials', placeholder);
}

export function maskBilling(value: string | number | null | undefined, placeholder: string = MASK_PLACEHOLDER): string {
  return mask(value, 'billing', placeholder);
}

export function maskLog(value: string | number | null | undefined, placeholder: string = MASK_PLACEHOLDER): string {
  return mask(value, 'logs', placeholder);
}
