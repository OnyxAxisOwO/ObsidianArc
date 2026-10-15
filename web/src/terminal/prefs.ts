// The terminal's appearance preferences: CONTRACT.md #5.4.
//
// Deliberately absent: opacity and blur. Those follow the global wallpaper
// setting (`--ai-surface-opacity` / `--ai-surface-filter`, set by
// `theme.ts:applyWallpaper()`), and AGENTS.md is explicit that the terminal
// must never define its own — so there is no field for either here.

export type TerminalCursorStyle = 'block' | 'bar' | 'underline';
export type TerminalScrollbackSize = 500 | 2000 | 10000;

export interface TerminalPrefs {
  /** A full CSS font-family stack, either a preset or hand-typed via "Custom…". */
  fontFamily: string;
  fontSize: number;
  lineHeight: number;
  letterSpacing: number;
  cursorStyle: TerminalCursorStyle;
  scrollback: TerminalScrollbackSize;
  /** A dim time next to each echoed command. */
  timestamps: boolean;
  ligatures: boolean;
}

const STORAGE_KEY = 'obsidian-arc-terminal-prefs';

/** Matches `.oa-field textarea`'s own default stack (`_workspace.scss`), so a terminal that has never been customised looks like every other monospace field in the project. */
export const DEFAULT_TERMINAL_FONT_FAMILY = 'ui-monospace, SFMono-Regular, Menlo, Consolas, monospace';

/**
 * Preset stacks for the font-family select. Values only — the "Custom…"
 * option and every label are the component's concern (a `StringKey` table
 * resolved with `t()` at render, the way `AdminPage.vue`'s `PAGES` is),
 * because this module must not import `i18n.ts` for a table that is
 * evaluated before the language is known.
 */
export const TERMINAL_FONT_FAMILY_PRESETS: readonly string[] = [
  DEFAULT_TERMINAL_FONT_FAMILY,
  '"Cascadia Code", "Cascadia Mono", ui-monospace, SFMono-Regular, Menlo, Consolas, monospace',
  '"JetBrains Mono", ui-monospace, SFMono-Regular, Menlo, Consolas, monospace',
  '"Fira Code", ui-monospace, SFMono-Regular, Menlo, Consolas, monospace',
  '"SF Mono", ui-monospace, Menlo, Consolas, monospace',
  'Consolas, "Courier New", monospace',
];

export const TERMINAL_CURSOR_STYLES: readonly TerminalCursorStyle[] = ['block', 'bar', 'underline'];
export const TERMINAL_SCROLLBACK_SIZES: readonly TerminalScrollbackSize[] = [500, 2000, 10000];

const FONT_SIZE_MIN = 11;
const FONT_SIZE_MAX = 20;
const LINE_HEIGHT_MIN = 1.2;
const LINE_HEIGHT_MAX = 2.0;
const LETTER_SPACING_MIN = 0;
const LETTER_SPACING_MAX = 2;
/**
 * A hand-edited localStorage value is never HTML, but it is spliced straight into an inline style; a length cap is enough to stop a font-family field from becoming a multi-kilobyte string. The settings field enforces the same cap as it is typed. It never commits a blank stack, and a blank one that arrives anyway renders with the default monospace stack, live and after a reload, rather than inheriting the UI's proportional font.
 */
export const FONT_FAMILY_MAX_LENGTH = 300;

export const TERMINAL_PREFS_DEFAULTS: Readonly<TerminalPrefs> = Object.freeze({
  fontFamily: DEFAULT_TERMINAL_FONT_FAMILY,
  fontSize: 13,
  lineHeight: 1.5,
  letterSpacing: 0,
  cursorStyle: 'bar',
  scrollback: 2000,
  timestamps: false,
  ligatures: false,
});

function clampNumber(value: unknown, min: number, max: number, fallback: number): number {
  // Number(null) and Number('') are 0, which is finite, so an absent or blank
  // stored value would clamp to the minimum instead of the default. Only a
  // number, or a string that is not blank, is read as one at all.
  if (typeof value !== 'number' && (typeof value !== 'string' || value.trim() === '')) return fallback;
  const n = typeof value === 'number' ? value : Number(value);
  if (!Number.isFinite(n)) return fallback;
  return Math.min(max, Math.max(min, n));
}

function clampEnum<T extends string>(value: unknown, allowed: readonly T[], fallback: T): T {
  return typeof value === 'string' && (allowed as readonly string[]).includes(value) ? (value as T) : fallback;
}

function clampScrollback(value: unknown, fallback: TerminalScrollbackSize): TerminalScrollbackSize {
  const n = typeof value === 'number' ? value : Number(value);
  return (TERMINAL_SCROLLBACK_SIZES as readonly number[]).includes(n) ? (n as TerminalScrollbackSize) : fallback;
}

function clampFontFamily(value: unknown, fallback: string): string {
  return typeof value === 'string' && value.length > 0 && value.length <= FONT_FAMILY_MAX_LENGTH ? value : fallback;
}

function clampBoolean(value: unknown, fallback: boolean): boolean {
  return typeof value === 'boolean' ? value : fallback;
}

/**
 * Reads the stored prefs, clamping every numeric or enum field into range
 * and falling back to the default for anything absent, unparseable, or
 * hand-edited into an out-of-range shape — a 400px font from a tampered
 * `localStorage` value must come back as the max (20), not survive as-is.
 */
export function loadTerminalPrefs(): TerminalPrefs {
  const defaults = TERMINAL_PREFS_DEFAULTS;
  let raw: unknown = null;
  try {
    const item = localStorage.getItem(STORAGE_KEY);
    raw = item === null ? null : JSON.parse(item);
  } catch {
    // A private window throws on `getItem`; a hand-edited value throws on
    // `JSON.parse`. Either way, the defaults below are the answer.
    raw = null;
  }

  const parsed = (raw !== null && typeof raw === 'object' ? raw : {}) as Partial<Record<keyof TerminalPrefs, unknown>>;

  return {
    fontFamily: clampFontFamily(parsed.fontFamily, defaults.fontFamily),
    fontSize: clampNumber(parsed.fontSize, FONT_SIZE_MIN, FONT_SIZE_MAX, defaults.fontSize),
    lineHeight: clampNumber(parsed.lineHeight, LINE_HEIGHT_MIN, LINE_HEIGHT_MAX, defaults.lineHeight),
    letterSpacing: clampNumber(parsed.letterSpacing, LETTER_SPACING_MIN, LETTER_SPACING_MAX, defaults.letterSpacing),
    cursorStyle: clampEnum(parsed.cursorStyle, TERMINAL_CURSOR_STYLES, defaults.cursorStyle),
    scrollback: clampScrollback(parsed.scrollback, defaults.scrollback),
    timestamps: clampBoolean(parsed.timestamps, defaults.timestamps),
    ligatures: clampBoolean(parsed.ligatures, defaults.ligatures),
  };
}

/** Best-effort: a private window that throws on `setItem` still gets the prefs applied for this tab's lifetime, it just does not remember them. */
export function saveTerminalPrefs(prefs: TerminalPrefs): void {
  try {
    localStorage.setItem(STORAGE_KEY, JSON.stringify(prefs));
  } catch {
    // Nothing to do — see the comment above.
  }
}

/**
 * The CSS custom properties these prefs set on the terminal root, in the
 * same spirit as `OaRangeField` setting `--oa-range-progress`: a
 * user-chosen quantity, not a palette entry, so it is a `--oa-term-*` name
 * rather than one of the shared `--ai-*` tokens.
 */
export function terminalCSSVariables(prefs: TerminalPrefs): Record<string, string> {
  return {
    '--oa-term-font-family': prefs.fontFamily.trim() ? prefs.fontFamily : DEFAULT_TERMINAL_FONT_FAMILY,
    '--oa-term-font-size': `${prefs.fontSize}px`,
    '--oa-term-line-height': String(prefs.lineHeight),
    '--oa-term-letter-spacing': `${prefs.letterSpacing}px`,
    // caret-shape spells the underline caret `underscore`; the stored value
    // keeps the name the settings page offers, so only the CSS side maps it.
    '--oa-term-cursor-style': prefs.cursorStyle === 'underline' ? 'underscore' : prefs.cursorStyle,
    '--oa-term-ligatures': prefs.ligatures ? 'normal' : 'none',
  };
}
