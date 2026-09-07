// Who is signed in, held in one place.
//
// The session is resolved once at boot and then kept in memory: every screen
// reads these refs rather than calling /api/auth/me again, so navigating
// between chat, settings and the admin pages costs no requests.
//
// Refs rather than the module-level `let`s this replaces, and no store
// library: three values and six functions do not need Pinia, and Vue's own
// reactivity is what makes the header redraw when a profile is saved.

import { computed, ref, type ComputedRef, type Ref } from 'vue';
import { ApiError } from '@/api/client';
import { fetchMe, fetchSite, savePreferences, type Account, type Preferences, type SiteInfo } from '@/api/auth';
import {
  accentPreference,
  setAccentPreference,
  setThemeMode,
  setWallpaper,
  themeMode,
  type ThemeMode,
  type Wallpaper,
} from '@/theme/theme';
import type { AccentName } from '@/theme/color-utils';

const account = ref<Account | null>(null);
const preferences = ref<Preferences>({});
/**
 * The instance's public settings, as the server described them.
 *
 * Exported so a test can stand an instance up — a landing page configured one
 * way is a different screen from the same page configured another, and there
 * is no other way in.
 */
export const site = ref<SiteInfo | null>(null);

/** What a server that has not answered yet is assumed to have said. */
const FALLBACK_SITE: SiteInfo = {
  name: 'Obsidian Arc',
  description: '',
  registration_enabled: false,
  setup_required: false,
  require_email: false,
  email_domains: [],
  verify_email: false,
  require_qq: false,
  qq_requirement: 'off',
  turnstile_on_login: false,
  turnstile_on_signup: false,
  turnstile_on_api_key: false,
  landing: { mode: 'login', intro: '', trial: false, trial_turns: 0 },
  about: { title: '', body: '' },
  home_notice: { text: '', dismissible: true },
};

export const currentUser: Ref<Account | null> = account;
export const currentPreferences: Ref<Preferences> = preferences;

export const isAdmin: ComputedRef<boolean> = computed(() => account.value?.role === 'admin');
export const siteInfo: ComputedRef<SiteInfo> = computed(() => site.value ?? FALLBACK_SITE);

export function requireUser(): Account {
  if (!account.value) throw new Error('session: no signed-in user');
  return account.value;
}

// Called after a successful sign-in or sign-up.
export function adopt(next: Account, prefs: Preferences = {}): void {
  account.value = next;
  preferences.value = prefs;
  applyServerPreferences(prefs);
}

export function forget(): void {
  account.value = null;
  preferences.value = {};
}

// Resolves the session and the instance's public settings in one round trip
// pair at startup. A 401 is the expected answer for a signed-out visitor, not
// an error worth surfacing.
export async function startSession(): Promise<void> {
  const [me, info] = await Promise.allSettled([fetchMe(), fetchSite()]);

  if (me.status === 'fulfilled') {
    account.value = me.value.user;
    preferences.value = me.value.preferences ?? {};
    applyServerPreferences(preferences.value);
  } else if (!(me.reason instanceof ApiError && me.reason.isAuth)) {
    // A network failure is worth knowing about; a 401 is not.
    console.warn('session lookup failed', me.reason);
  }

  if (info.status === 'fulfilled') site.value = info.value;
}

// The account's stored theme and accent win over whatever this browser had,
// so signing in on a new device brings the interface with it. Applied only
// when the server actually has a value: a fresh account should not reset a
// choice made before signing in.
function applyServerPreferences(prefs: Preferences): void {
  const theme = prefs['theme'];
  if (theme === 'light' || theme === 'dark' || theme === 'auto') {
    setThemeMode(theme);
  }

  const accent = prefs['accent'];
  const custom = prefs['custom_accent'];
  if (typeof accent === 'string') {
    setAccentPreference({
      accent: accent as AccentName | 'custom',
      customAccent: typeof custom === 'string' ? custom : '',
    });
  }

  // The wallpaper itself lives on the server; the preference carries the URL
  // it is served from, plus how it should be dimmed and blurred.
  const paper = prefs['wallpaper'];
  if (paper === null) {
    setWallpaper(null);
  } else if (paper && typeof paper === 'object' && typeof (paper as Wallpaper).url === 'string') {
    const value = paper as Wallpaper;
    setWallpaper({
      url: value.url,
      dim: value.dim ?? 0,
      blur: value.blur ?? 0,
      translucency: value.translucency ?? 0,
      panelBlur: value.panelBlur ?? 0,
    });
  }
}

// Mirrors a local preference change back to the account. Fire and forget:
// the setting has already been applied locally, and a failed sync should not
// interrupt what the user was doing.
export function syncPreferences(patch: Preferences): void {
  if (!account.value) return;
  preferences.value = { ...preferences.value, ...patch };
  void savePreferences(patch).catch(() => {
    // Offline, or the session expired. The local value stands.
  });
}

// Convenience for the theme toggle, which lives in the shared header and has
// to work whether or not anyone is signed in.
export function persistTheme(mode: ThemeMode): void {
  setThemeMode(mode);
  syncPreferences({ theme: mode });
}

export function persistAccent(accent: AccentName | 'custom', customAccent: string): void {
  setAccentPreference({ accent, customAccent });
  syncPreferences({ accent, custom_accent: customAccent });
}

export { themeMode, accentPreference };
