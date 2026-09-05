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
const WALLPAPER_KEY = 'obsidian-arc-wallpaper';

export type ThemeMode = 'light' | 'dark' | 'auto';

export interface Wallpaper {
  // A same-origin URL or a data: URI. Anything else is ignored: it would be a
  // cross-origin request the page made on a stored value's say-so.
  url: string;
  // 0-100. Dims the wallpaper so interface text stays readable over it.
  dim: number;
  blur: number;
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

export function themeMode(): ThemeMode {
  const stored = read(THEME_KEY);
  return stored === 'light' || stored === 'dark' ? stored : 'auto';
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

function applyTheme(): void {
  const root = document.documentElement;
  const mode = themeMode();
  if (mode === 'auto') root.removeAttribute('data-theme');
  else root.setAttribute('data-theme', mode);
  // Which lightness the accent hue is clamped to depends on the resolved
  // scheme, not just on which hue was picked, so a theme switch has to
  // re-derive it.
  applyAccent();
}

// --- accent ----------------------------------------------------------------

export function accentPreference(): AccentPreference {
  const fallback: AccentPreference = { accent: DEFAULT_ACCENT, customAccent: '' };
  const raw = read(ACCENT_KEY);
  if (!raw) return fallback;
  try {
    const parsed = JSON.parse(raw) as Partial<AccentPreference>;
    const accent = parsed.accent;
    const valid = accent === 'custom' || (typeof accent === 'string' && accent in ACCENTS);
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

function applyAccent(): void {
  const pref = accentPreference();
  const base = baseAccent(pref);
  const dark = isDark();

  // The neutral preset skips the generic dark-mode lightness lift and goes
  // straight to white — the lift turns a true black/white pick into a dull
  // grey, the same special case PageDye's own picker makes.
  const primary = dark && pref.accent === 'neutral' ? '#FFFFFF' : displayAccent(base, dark);
  const onPrimary = colorIsLight(primary) ? '#000000' : '#FFFFFF';
  const hover = shiftHex(primary, colorIsLight(primary) ? -32 : 24);

  const style = document.documentElement.style;
  style.setProperty('--ai-primary', primary);
  style.setProperty('--ai-primary-text', onPrimary);
  style.setProperty('--ai-primary-hover', hover);
  style.setProperty('--ai-focus-shadow', hexToRgba(primary, dark ? 0.28 : 0.22));
  // The generic hover/press layer shared by nearly every button, chip, menu
  // item and list row. Tinting this is most of what makes the interface read
  // as "in this colour" rather than "has a few coloured buttons".
  style.setProperty('--ai-state-hover', hexToRgba(primary, dark ? 0.16 : 0.08));
  style.setProperty('--ai-badge-bg', hexToRgba(primary, dark ? 0.16 : 0.12));
  style.setProperty('--ai-badge-text', primary);

  notify();
}

// --- wallpaper -------------------------------------------------------------

export function wallpaper(): Wallpaper | null {
  const raw = read(WALLPAPER_KEY);
  if (!raw) return null;
  try {
    const parsed = JSON.parse(raw) as Partial<Wallpaper>;
    if (typeof parsed.url !== 'string' || !safeWallpaperURL(parsed.url)) return null;
    return {
      url: parsed.url,
      dim: clamp(parsed.dim ?? 0, 0, 100),
      blur: clamp(parsed.blur ?? 0, 0, 40),
    };
  } catch {
    return null;
  }
}

export function setWallpaper(next: Wallpaper | null): void {
  write(WALLPAPER_KEY, next ? JSON.stringify(next) : '');
  applyWallpaper();
}

// Only a same-origin path or an inline image. A stored absolute URL would let
// anything that could write localStorage turn every page load into a request
// to a server of its choosing.
function safeWallpaperURL(url: string): boolean {
  if (url.startsWith('/')) return !url.startsWith('//');
  return /^data:image\/(?:png|jpeg|webp|gif|avif);base64,[A-Za-z0-9+/]+=*$/.test(url);
}

function applyWallpaper(): void {
  const root = document.documentElement;
  const style = root.style;
  const current = wallpaper();
  root.classList.toggle('has-wallpaper', current !== null);
  if (!current) {
    style.removeProperty('--ai-wallpaper');
    style.removeProperty('--ai-wallpaper-dim');
    style.removeProperty('--ai-wallpaper-blur');
    return;
  }
  style.setProperty('--ai-wallpaper', `url("${cssEscape(current.url)}")`);
  style.setProperty('--ai-wallpaper-dim', String(current.dim / 100));
  style.setProperty('--ai-wallpaper-blur', `${current.blur}px`);
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
    if (themeMode() === 'auto') applyAccent();
  });
}
