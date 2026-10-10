// Theme and accent, lifted out of the standalone workspace module so every
// page — chat, login, admin — is painted by the same code.
//
// Both preferences live in localStorage rather than in a store: they must be
// readable before the first paint and before any network call, including on
// the login page where there is no account yet. Once a session exists they
// are also mirrored to the server (see api/preferences) so the choice
// follows the account to another device; localStorage stays the fast path and
// the offline fallback.

import {
  ACCENTS,
  DEFAULT_ACCENT,
  accentPalette,
  baseAccent,
  colorIsLight,
  displayAccent,
  hexToRgba,
  normalizeHex,
  shiftHex,
  type AccentName,
  type AccentPreference,
} from './color-utils';

const THEME_KEY = 'obsidian-arc-theme';
const ACCENT_KEY = 'obsidian-arc-accent';
const BACKGROUND_KEY = 'obsidian-arc-background-accent';
const WALLPAPER_KEY = 'obsidian-arc-wallpaper';
// The resolved palette, cached for the inline script in index.html: it has to
// paint the accent's surfaces before the module loads, and duplicating the
// colour maths in a blocking script would be worse than storing the answer.
const PALETTE_KEY = 'obsidian-arc-palette';
// The instance's own theme, as /api/site last described it. Kept here for the
// same reason the palette is: the next load paints before it has asked.
const SITE_THEME_KEY = 'obsidian-arc-site-theme';
// The scheme actually in force once the instance's default is folded in, for
// the inline script, which reads it before the reader's own choice.
const EFFECTIVE_THEME_KEY = 'obsidian-arc-effective-theme';

export type ThemeMode = 'light' | 'dark' | 'auto';

export interface Wallpaper {
  // A same-origin URL or a data: URI. Anything else is ignored: it would be a
  // cross-origin request the page made on a stored value's say-so.
  url: string;
  // 0-100. Dims the wallpaper so interface text stays readable over it.
  dim: number;
  blur: number;
  // 0-90. How far the interface's own panels are seen through. Capped short
  // of 100 because a card at nothing is text lying loose on a photograph.
  translucency: number;
  // 0-40px, behind those panels rather than over the wallpaper itself: it is
  // what keeps the picture from competing with the words on top of it.
  panelBlur: number;
}

// What an operator set for the whole instance (theme.* in the settings
// store). Each field is a default for a reader who has not chosen; with
// `enforce` it is the choice, and the reader's own steps aside.
export interface SiteTheme {
  mode: ThemeMode;
  accent: string;
  custom_accent: string;
  background_accent: string;
  dim: number;
  blur: number;
  translucency: number;
  panel_blur: number;
  enforce: boolean;
}

// One stored background, as /api/site lists it.
export interface SiteBackground {
  url: string;
  html: boolean;
}

// The wallpaper actually painted: the reader's own, or the instance's.
// `html` is a page drawn in a frame rather than a picture (see SiteBackdrop).
export interface PaintedWallpaper extends Wallpaper {
  html: boolean;
}

type Listener = () => void;

const listeners = new Set<Listener>();

export function onThemeChange(listener: Listener): () => void {
  listeners.add(listener);
  return () => listeners.delete(listener);
}

function notify(): void {
  for (const listener of listeners) {
    try {
      listener();
    } catch {
      // One subscriber's bug must not stop the others from repainting.
    }
  }
}

function read(key: string): string | null {
  try {
    return localStorage.getItem(key);
  } catch {
    // Storage disabled (a private window with site data blocked): behave as
    // though nothing was ever saved.
    return null;
  }
}

function write(key: string, value: string): void {
  try {
    localStorage.setItem(key, value);
  } catch {
    // Best effort. The setting still applies for this page load.
  }
}

// --- theme -----------------------------------------------------------------

// --- the instance's theme ----------------------------------------------------

let site: SiteTheme | null = readSiteTheme();
let siteBackgrounds: Record<string, SiteBackground> = {};
// Whether anyone is signed in. The instance's signed-in background is that,
// and the sign-in pages draw their own.
let signedIn = false;

function readSiteTheme(): SiteTheme | null {
  const raw = read(SITE_THEME_KEY);
  if (!raw) return null;
  try {
    return normalizeSiteTheme(JSON.parse(raw));
  } catch {
    return null;
  }
}

function normalizeSiteTheme(value: unknown): SiteTheme | null {
  if (!value || typeof value !== 'object') return null;
  const v = value as Partial<SiteTheme>;
  const mode = v.mode === 'light' || v.mode === 'dark' ? v.mode : 'auto';
  const accent = typeof v.accent === 'string' && (v.accent === 'custom' || Object.hasOwn(ACCENTS, v.accent)) ? v.accent : '';
  const tint = typeof v.background_accent === 'string' && Object.hasOwn(ACCENTS, v.background_accent) ? v.background_accent : '';
  return {
    mode,
    accent,
    custom_accent: normalizeHex(v.custom_accent, '') ?? '',
    background_accent: tint,
    dim: clamp(Number(v.dim ?? 0), 0, 100),
    blur: clamp(Number(v.blur ?? 0), 0, 40),
    translucency: clamp(Number(v.translucency ?? 0), 0, 90),
    panel_blur: clamp(Number(v.panel_blur ?? 0), 0, 40),
    enforce: v.enforce === true,
  };
}

export function siteTheme(): SiteTheme | null {
  return site;
}

/** Whether the instance has taken the reader's appearance settings over. */
export function themeEnforced(): boolean {
  return site?.enforce === true;
}

/** Whether the light/dark switch is the instance's rather than the reader's. */
export function modeLocked(): boolean {
  return themeEnforced() && site?.mode !== 'auto';
}

/**
 * Adopts what /api/site said about the instance's look. Called on every
 * answer, including after an operator saves the settings page, so the tab
 * that did it repaints without a reload.
 */
export function setSiteAppearance(theme: unknown, backgrounds: Record<string, SiteBackground> | undefined): void {
  site = normalizeSiteTheme(theme);
  write(SITE_THEME_KEY, site ? JSON.stringify(site) : '');
  siteBackgrounds = backgrounds ?? {};
  applyTheme();
  applyWallpaper();
  notify();
}

export function setSignedIn(next: boolean): void {
  if (signedIn === next) return;
  signedIn = next;
  applyWallpaper();
  notify();
}

/** The three shapes of screen a background is set for. */
export type ScreenKind = 'desktop' | 'tablet' | 'phone';

// A tablet is a touch screen with room on both sides, held either way: a
// phone on its side is under 600px tall and stays a phone-or-desktop
// question for orientation to answer, as it always has.
export const TABLET_QUERY = '(pointer: coarse) and (min-width: 600px) and (min-height: 600px)';
export const PORTRAIT_QUERY = '(orientation: portrait)';

export function screenKind(tablet: boolean, portrait: boolean): ScreenKind {
  if (tablet) return 'tablet';
  return portrait ? 'phone' : 'desktop';
}

// Which stored shape stands in for which, nearest first: a tablet borrows
// the desktop picture before the phone's, and each of the other two borrows
// the tablet's before the other's, since a tablet sits between them.
const SHAPES: Record<ScreenKind, string[]> = {
  desktop: ['landscape', 'tablet', 'portrait'],
  tablet: ['tablet', 'landscape', 'portrait'],
  phone: ['portrait', 'tablet', 'landscape'],
};

/**
 * Picks the variant for this screen from a set, falling back the way a
 * reader would least notice: another shape in the same brightness before the
 * same shape in the other one, since a light picture behind dark panels is
 * the worse mismatch.
 */
export function pickBackground(
  set: Record<string, SiteBackground> | undefined,
  prefix: '' | 'app_',
  screen: ScreenKind,
  dark: boolean,
): SiteBackground | null {
  if (!set) return null;
  const tones = dark ? ['dark', 'light'] : ['light', 'dark'];
  for (const tone of tones) {
    for (const shape of SHAPES[screen]) {
      const found = set[`${prefix}${shape}_${tone}`];
      if (found && typeof found.url === 'string' && found.url.startsWith('/') && !found.url.startsWith('//')) return found;
    }
  }
  return null;
}

function currentScreen(): ScreenKind {
  const query = (q: string) => window.matchMedia?.(q).matches ?? false;
  return screenKind(query(TABLET_QUERY), query(PORTRAIT_QUERY));
}

// --- theme -----------------------------------------------------------------

/** The scheme the reader picked themselves, or null for none. */
function ownThemeMode(): ThemeMode | null {
  const stored = read(THEME_KEY);
  return stored === 'light' || stored === 'dark' || stored === 'auto' ? stored : null;
}

export function themeMode(): ThemeMode {
  if (modeLocked() && site) return site.mode;
  return ownThemeMode() ?? site?.mode ?? 'auto';
}

export function isDark(): boolean {
  const mode = themeMode();
  if (mode === 'dark') return true;
  if (mode === 'light') return false;
  return window.matchMedia?.('(prefers-color-scheme: dark)').matches ?? false;
}

export function setThemeMode(mode: ThemeMode): void {
  write(THEME_KEY, mode);
  applyTheme();
}

// The order light → dark → auto that the header button cycles through.
export function nextThemeMode(mode: ThemeMode = themeMode()): ThemeMode {
  return ({ auto: 'light', light: 'dark', dark: 'auto' } as const)[mode];
}

// Cleared and reset rather than stacked: switching twice quickly should end
// with one timer, not two racing to take the class off.
let switching = 0;

/**
 * Marks the root for the length of a theme change, so the stylesheet can
 * animate every colour at once instead of the handful of rules that happen to
 * carry a transition of their own.
 *
 * Not applied on the first paint: the class is only added once a theme is
 * already on screen, so a page loading dark does not fade into itself.
 */
function markSwitching(root: HTMLElement): void {
  if (!started) return;
  root.classList.add('theme-switching');
  window.clearTimeout(switching);
  switching = window.setTimeout(() => root.classList.remove('theme-switching'), 240);
}

function repaintWallpaper(): void {
  const before = lastWallpaper;
  applyWallpaper();
  if (lastWallpaper !== before) notify();
}

function applyTheme(): void {
  const root = document.documentElement;
  const mode = themeMode();
  markSwitching(root);
  if (mode === 'auto') root.removeAttribute('data-theme');
  else root.setAttribute('data-theme', mode);
  write(EFFECTIVE_THEME_KEY, mode);
  // Which lightness the accent hue is clamped to depends on the resolved
  // scheme, not just on which hue was picked, so a theme switch has to
  // re-derive it.
  applyAccent();
  // And the instance's background comes in a light and a dark variant.
  if (started) repaintWallpaper();
}

// --- accent ----------------------------------------------------------------

function siteAccent(): AccentPreference | null {
  if (!site?.accent) return null;
  if (site.accent === 'custom' && !site.custom_accent) return null;
  return { accent: site.accent as AccentName | 'custom', customAccent: site.custom_accent };
}

export function accentPreference(): AccentPreference {
  const instance = siteAccent();
  if (themeEnforced()) return instance ?? { accent: DEFAULT_ACCENT, customAccent: '' };
  const fallback: AccentPreference = instance ?? { accent: DEFAULT_ACCENT, customAccent: '' };
  const raw = read(ACCENT_KEY);
  if (!raw) return fallback;
  try {
    const parsed = JSON.parse(raw) as Partial<AccentPreference>;
    const accent = parsed.accent;
    // `in` also accepts what every object inherits (constructor, __proto__).
    // Such a name would pass here and then crash the palette on every boot,
    // since the value is stored on the account and read back each session.
    const valid = accent === 'custom' || (typeof accent === 'string' && Object.hasOwn(ACCENTS, accent));
    return {
      accent: valid ? (accent as AccentName | 'custom') : fallback.accent,
      customAccent: normalizeHex(parsed.customAccent, '') ?? '',
    };
  } catch {
    return fallback;
  }
}

export function setAccentPreference(pref: AccentPreference): void {
  write(ACCENT_KEY, JSON.stringify(pref));
  applyAccent();
}

export function backgroundAccent(): AccentName | '' {
  const instance = (site?.background_accent ?? '') as AccentName | '';
  if (themeEnforced()) return instance;
  const value = read(BACKGROUND_KEY);
  // Null is "never chosen", which takes the instance's; an empty string is
  // the reader choosing none, which stands.
  if (value === null) return instance;
  return value && Object.hasOwn(ACCENTS, value) ? value as AccentName : '';
}

export function setBackgroundAccent(value: AccentName | ''): void {
  write(BACKGROUND_KEY, value);
  applyAccent();
}

// Every token the accent decides, for one hue and one scheme. Returned rather
// than applied so the same function can produce the light and dark palettes
// the pre-paint cache needs, not just the one being shown.
function paletteFor(pref: AccentPreference, dark: boolean): Record<string, string> {
  const base = baseAccent(pref);

  // The neutral preset skips the generic dark-mode lightness lift and goes
  // straight to white — the lift turns a true black/white pick into a dull
  // grey, the same special case PageDye's own picker makes.
  const primary = dark && pref.accent === 'neutral' ? '#FFFFFF' : displayAccent(base, dark);
  const onPrimary = colorIsLight(primary) ? '#000000' : '#FFFFFF';
  const hover = shiftHex(primary, colorIsLight(primary) ? -32 : 24);

  return {
    // The surfaces, borders and text: the accent hue is the interface's hue,
    // not a colour applied on top of a violet one.
    // Background and controls are separate choices; both use the same ramp
    // so a pastel background remains legible in either display mode.
    ...accentPalette(backgroundAccent() ? ACCENTS[backgroundAccent() as AccentName] : base, dark),
    '--ai-primary': primary,
    '--ai-primary-text': onPrimary,
    '--ai-primary-hover': hover,
    '--ai-focus-shadow': hexToRgba(primary, dark ? 0.28 : 0.22),
    // The generic hover/press layer shared by nearly every button, chip, menu
    // item and list row.
    '--ai-state-hover': hexToRgba(primary, dark ? 0.16 : 0.08),
    '--ai-badge-bg': hexToRgba(primary, dark ? 0.16 : 0.12),
    '--ai-badge-text': primary,
  };
}

function applyAccent(): void {
  const pref = accentPreference();
  const dark = isDark();

  const style = document.documentElement.style;
  for (const [token, value] of Object.entries(paletteFor(pref, dark))) {
    style.setProperty(token, value);
  }

  // Both schemes are cached, because which one applies on the next load
  // depends on a system preference that can change while this tab is closed.
  write(PALETTE_KEY, JSON.stringify({
    light: declarations(paletteFor(pref, false)),
    dark: declarations(paletteFor(pref, true)),
  }));

  notify();
}

function declarations(palette: Record<string, string>): string {
  return Object.entries(palette).map(([token, value]) => `${token}:${value}`).join(';');
}

// --- wallpaper -------------------------------------------------------------

/**
 * What is painted behind the interface: the reader's own picture, unless the
 * instance has taken appearance over or the reader has none — then the
 * instance's signed-in background, if it has one for this screen.
 */
export function wallpaper(): PaintedWallpaper | null {
  const own = themeEnforced() ? null : ownWallpaper();
  if (own) return { ...own, html: false };
  if (!signedIn || !site) return null;
  const picked = pickBackground(siteBackgrounds, 'app_', currentScreen(), isDark());
  if (!picked) return null;
  return {
    url: picked.url,
    html: picked.html,
    dim: site.dim,
    blur: site.blur,
    translucency: site.translucency,
    panelBlur: site.panel_blur,
  };
}

/** The reader's own wallpaper, whatever the instance paints instead. */
export function ownWallpaper(): Wallpaper | null {
  const raw = read(WALLPAPER_KEY);
  if (!raw) return null;
  try {
    const parsed = JSON.parse(raw) as Partial<Wallpaper>;
    if (typeof parsed.url !== 'string' || !safeWallpaperURL(parsed.url)) return null;
    return {
      url: parsed.url,
      dim: clamp(parsed.dim ?? 0, 0, 100),
      blur: clamp(parsed.blur ?? 0, 0, 40),
      // Zero for a wallpaper stored before these existed, which is the look
      // it already has.
      translucency: clamp(parsed.translucency ?? 0, 0, 90),
      panelBlur: clamp(parsed.panelBlur ?? 0, 0, 40),
    };
  } catch {
    return null;
  }
}

export function setWallpaper(next: Wallpaper | null): void {
  write(WALLPAPER_KEY, next ? JSON.stringify(next) : '');
  applyWallpaper();
  notify();
}

// Only a same-origin path or an inline image. A stored absolute URL would let
// anything that could write localStorage turn every page load into a request
// to a server of its choosing.
function safeWallpaperURL(url: string): boolean {
  if (url.startsWith('/')) return !url.startsWith('//');
  return /^data:image\/(?:png|jpeg|webp|gif|avif);base64,[A-Za-z0-9+/]+=*$/.test(url);
}

// Which wallpaper is on screen, so a change of scheme or orientation that
// picks a different instance variant knows to tell the listeners.
let lastWallpaper = '';

function applyWallpaper(): void {
  const root = document.documentElement;
  const style = root.style;
  const current = wallpaper();
  root.classList.toggle('has-wallpaper', current !== null);
  if (!current) {
    lastWallpaper = '';
    style.removeProperty('--ai-wallpaper');
    style.removeProperty('--ai-wallpaper-dim');
    style.removeProperty('--ai-wallpaper-blur');
    style.removeProperty('--ai-surface-opacity');
    style.removeProperty('--ai-surface-filter');
    return;
  }
  lastWallpaper = current.url;
  // A page is drawn by SiteBackdrop in a frame; the picture layer stays
  // empty under it, and the dim and blur below apply to both.
  if (current.html) style.removeProperty('--ai-wallpaper');
  else style.setProperty('--ai-wallpaper', `url("${cssEscape(current.url)}")`);
  style.setProperty('--ai-wallpaper-dim', String(current.dim / 100));
  style.setProperty('--ai-wallpaper-blur', `${current.blur}px`);
  // Opacity rather than translucency, as a percentage the stylesheet can
  // hand straight to color-mix: the setting reads "how far through", the
  // paint needs "how much is left".
  style.setProperty('--ai-surface-opacity', `${100 - current.translucency}%`);
  style.setProperty('--ai-surface-filter', current.panelBlur > 0 ? `blur(${current.panelBlur}px)` : 'none');
}

function cssEscape(value: string): string {
  return value.replace(/["\\]/g, '\\$&');
}

function clamp(value: number, low: number, high: number): number {
  if (!Number.isFinite(value)) return low;
  return Math.min(high, Math.max(low, value));
}

// --- boot ------------------------------------------------------------------

let started = false;

// Called once at startup. The inline script in index.html has already set
// data-theme to avoid a flash; this fills in everything that needs real code.
export function startTheme(): void {
  if (started) return;
  started = true;
  applyTheme();
  applyWallpaper();
  window.matchMedia?.('(prefers-color-scheme: dark)').addEventListener('change', () => {
    if (themeMode() === 'auto') applyTheme();
  });
  // The instance's background comes in a shape per screen, so turning a
  // phone, or docking a tablet to a keyboard, may want another picture.
  window.matchMedia?.(PORTRAIT_QUERY).addEventListener('change', repaintWallpaper);
  window.matchMedia?.(TABLET_QUERY).addEventListener('change', repaintWallpaper);
}
