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
import { SESSION_ENDED, SESSION_LOST } from '@/api/session-ended';
import { fetchMe, fetchSite, savePreferences, type Account, type Preferences, type SiteInfo } from '@/api/auth';
import { loadPlugins } from '@/plugins/registry';
import { changeLanguage, currentLanguage } from '@/composables/useI18n';
import { within } from '@/lib/deadline';
import {
  accentPreference,
  setAccentPreference,
  setBackgroundAccent,
  setSignedIn,
  setSiteAppearance,
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
  browser_title: 'Obsidian Arc',
  registration_enabled: false,
  health_show_users: false,
  leaderboard_show_users: false,
  setup_required: false,
  require_email: false,
  email_domains: [],
  verify_email: false,
  fields: {},
  turnstile_on_login: false,
  turnstile_on_signup: false,
  turnstile_on_api_key: false,
  turnstile_on_feedback: false,
  turnstile_on_images: false,
  pow_on_images: false,
  turnstile_on_redeem: false,
  turnstile_on_chat_speed: false,
  oauth: [],
  landing: { mode: 'login', intro: '', trial: false, trial_turns: 0 },
  about: { title: '', body: '', show_software_info: true },
  home_notice: { text: '', dismissible: true, body: '', tone: 'info' },
  logo_url: '',
  auth_card_position: 'center',
  oauth_only_signup: false,
  oidc_only_signup: false,
  oauth_allow_password: true,
  oauth_require_password: false,
  oauth_require_username: false,
  oauth_oidc_require_completion: false,
};

/**
 * True while this browser holds a sign-in that proved the password and is
 * waiting for its two-step code. There is no account yet — everything but the
 * code step answers as if signed out — so it is kept apart from `account`.
 */
export const pendingSecondFactor = ref(false);

export const currentUser: Ref<Account | null> = account;
export const currentPreferences: Ref<Preferences> = preferences;

export const isSuperAdmin = computed(() => account.value?.role === 'super_admin');
export const isAdmin: ComputedRef<boolean> = computed(() => isSuperAdmin.value || account.value?.role === 'admin');
export function canAdmin(permission: string): boolean {
  return isSuperAdmin.value || (isAdmin.value && (account.value?.admin_permissions ?? []).includes(permission));
}
export const siteInfo: ComputedRef<SiteInfo> = computed(() => site.value ?? FALLBACK_SITE);

export function requireUser(): Account {
  if (!account.value) throw new Error('session: no signed-in user');
  return account.value;
}

// Called after a successful sign-in or sign-up.
export function adopt(next: Account, prefs: Preferences = {}): void {
  pendingSecondFactor.value = false;
  account.value = next;
  preferences.value = prefs;
  setSignedIn(true);
  void applyServerPreferences(prefs);
}

// Every store that holds one account's state registers how to clear it, and
// forget() runs them all. The stores are not imported here: chat/useChat.ts
// already imports this module, so the reverse import would be a cycle. A store
// that keeps per-account state and does not register here is the bug to look for.
const signOutResets: Array<() => void> = [];

/** Registers a reset that runs on every sign-out, in registration order. */
export function onSignOut(reset: () => void): void {
  signOutResets.push(reset);
}

// The server ends a session at its expiry, and an open tab learns of it only
// from a refused request. Left signed in, the page would keep its account screen
// and show the server's sentence on each action. Only a tab that believes
// somebody is signed in is signed out: an anonymous visitor's refusal is the
// answer they were always going to get.
//
// Signing out here is not enough on its own: nothing watches currentUser for a
// route, so the tab would stay on a protected page with no account behind it.
// SESSION_LOST tells the router to send it to sign-in, the way the two explicit
// sign-out paths already do with router.replace after forget().
if (typeof window !== 'undefined') {
  window.addEventListener(SESSION_ENDED, () => {
    if (account.value === null) return;
    forget();
    window.dispatchEvent(new Event(SESSION_LOST));
  });
}

export function forget(): void {
  pendingSecondFactor.value = false;
  account.value = null;
  preferences.value = {};
  setSignedIn(false);
  // The wallpaper belongs to the account and is kept here only so the first
  // paint can use it. Left behind, it keeps painting the last account's picture
  // on screens that have no account to draw it for.
  setWallpaper(null);
  forgetTerminalHistory();
  for (const reset of signOutResets) reset();
}

// What the terminal's prompt remembered belongs to the person who typed it,
// and the browser stays behind when they leave. Every account's list goes, not
// only the one signed out: this is the shared computer's last line of defence.
//
// The prefix is spelled out rather than imported from terminal/session.ts, as
// a static import of anything under terminal/ would pull that chunk into the
// main graph; test/terminal-plumbing.test.ts holds the two spellings together.
function forgetTerminalHistory(): void {
  try {
    const stored: string[] = [];
    for (let i = 0; i < localStorage.length; i += 1) {
      const key = localStorage.key(i);
      if (key !== null && key.startsWith('obsidian-arc-terminal-history')) stored.push(key);
    }
    for (const key of stored) localStorage.removeItem(key);
  } catch {
    // Storage may be blocked; then nothing was kept there either.
  }
}

// Long enough for a dictionary chunk on a slow link, short enough that a stalled
// one does not keep the page on its spinner.
const ACCOUNT_LANGUAGE_DEADLINE_MS = 3_000;

// Resolves the session and the instance's public settings in one round trip
// pair at startup. A 401 is the expected answer for a signed-out visitor, not
// an error worth surfacing.
export async function startSession(): Promise<void> {
  const [me, info] = await Promise.allSettled([fetchMe(), fetchSite()]);
  let languageReady: Promise<void> = Promise.resolve();

  if (me.status === 'fulfilled') {
    account.value = me.value.user;
    preferences.value = me.value.preferences ?? {};
    setSignedIn(true);
    languageReady = applyServerPreferences(preferences.value);
  } else if (me.reason instanceof ApiError && me.reason.code === 'two_factor_pending') {
    // Halfway through signing in — a provider sign-in lands here by
    // redirect. The sign-in page asks for the rest.
    pendingSecondFactor.value = true;
  } else if (!(me.reason instanceof ApiError && me.reason.isAuth)) {
    // A network failure is worth knowing about; a 401 is not.
    console.warn('session lookup failed', me.reason);
  }

  if (info.status === 'fulfilled') {
    site.value = info.value;
    setSiteAppearance(info.value.theme, info.value.backgrounds);
    // Before the first render, so a sign-up card that a plugin adds a field
    // to is drawn with the field rather than growing one a moment later.
    await loadPlugins(info.value.plugins);
  } else {
    // A failed /api/site answer still counts as an answer: no plugins are
    // coming, and the plugin screens must read "missing", not keep loading.
    await loadPlugins(undefined);
  }
  // Awaited last, so the first paint is in the account's language rather than
  // flashing the browser's before switching. Bounded, because boot awaits this
  // before mounting: a dictionary request that is accepted and never answers
  // would otherwise hold the whole page. The late dictionary still lands, and
  // changeLanguage redraws then.
  await within(languageReady, ACCOUNT_LANGUAGE_DEADLINE_MS).catch(() => undefined);
}

// Reads /api/site again and loads the plugins it now names — after the
// plugins screen switched one, so the backoffice that did it is the first to
// draw the change.
export async function refreshSite(): Promise<void> {
  const info = await fetchSite();
  site.value = info;
  setSiteAppearance(info.theme, info.backgrounds);
  await loadPlugins(info.plugins);
}

// The account's stored theme, accent and language win over whatever this
// browser had, so signing in on a new device brings the interface with it.
// Applied only when the server actually has a value: a fresh account should
// not reset a choice made before signing in.
//
// The language is the one step that can wait: its dictionary may have to be
// fetched. The promise never rejects, but it stays pending while a request
// stalls, which is why startSession bounds its wait on it; the page then
// stays in the language it was drawn in until the dictionary lands.
function applyServerPreferences(prefs: Preferences): Promise<void> {
  if (typeof prefs['background_accent'] === 'string') setBackgroundAccent(prefs['background_accent'] as AccentName | '');
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

  const language = prefs['language'];
  if ((language === 'en' || language === 'zh') && language !== currentLanguage()) {
    // The same limit primeLanguage gives the browser's language. startSession
    // bounds its own wait on this, but an accepted dictionary that never
    // answers would otherwise leave this promise pending for ever.
    return within(changeLanguage(language), 10_000).catch((failure: unknown) => {
      console.warn('account language did not load; the page stays in the browser language', failure);
    });
  }
  return Promise.resolve();
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
