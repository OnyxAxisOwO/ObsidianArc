// Who is signed in, held in one place.
//
// The session is resolved once at boot and then kept in memory: every screen
// asks this module rather than calling /api/auth/me again, so navigating
// between chat, settings and the admin pages costs no requests.

import { ApiError } from './api/client';
import { fetchMe, fetchSite, savePreferences, type Account, type Preferences, type SiteInfo } from './api/auth';
import {
  accentPreference,
  setAccentPreference,
  setThemeMode,
  themeMode,
  type ThemeMode,
} from './theme/theme';
import type { AccentName } from './theme/color-utils';

let account: Account | null = null;
let preferences: Preferences = {};
let site: SiteInfo | null = null;

export function currentUser(): Account | null {
  return account;
}

export function requireUser(): Account {
  if (!account) throw new Error('session: no signed-in user');
  return account;
}

export function isAdmin(): boolean {
  return account?.role === 'admin';
}

export function siteInfo(): SiteInfo {
  return site ?? { name: 'Obsidian Arc', description: '', registration_enabled: false, setup_required: false };
}

export function currentPreferences(): Preferences {
  return preferences;
}

// Called after a successful sign-in or sign-up.
export function adopt(next: Account, prefs: Preferences = {}): void {
  account = next;
  preferences = prefs;
  applyServerPreferences(prefs);
}

export function forget(): void {
  account = null;
  preferences = {};
}

// Resolves the session and the instance's public settings in one round trip
// pair at startup. A 401 is the expected answer for a signed-out visitor, not
// an error worth surfacing.
export async function start(): Promise<void> {
  const [me, info] = await Promise.allSettled([fetchMe(), fetchSite()]);

  if (me.status === 'fulfilled') {
    account = me.value.user;
    preferences = me.value.preferences ?? {};
    applyServerPreferences(preferences);
  } else if (!(me.reason instanceof ApiError && me.reason.isAuth)) {
    // A network failure is worth knowing about; a 401 is not.
    console.warn('session lookup failed', me.reason);
  }

  if (info.status === 'fulfilled') site = info.value;
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
}

// Mirrors a local preference change back to the account. Fire and forget:
// the setting has already been applied locally, and a failed sync should not
// interrupt what the user was doing.
export function syncPreferences(patch: Preferences): void {
  if (!account) return;
  preferences = { ...preferences, ...patch };
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
