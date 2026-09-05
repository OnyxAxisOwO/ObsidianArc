// The accent picker's colour maths, carried over from the standalone build
// (which took it from PageDye, the sibling project this interface grew out
// of) so the two keep the same "pick one hue, get a scheme that works in both
// light and dark" behaviour.
//
// A user picks ONE hex per accent — there is no separate light and dark
// colour to keep in step — and displayAccent clamps that hue's lightness per
// scheme before it is ever painted: light mode pulls it down to a
// readable-on-white shade, dark mode pushes it up to a readable-on-near-black
// one. That is what makes --ai-primary-text safe to fix at white or near
// black per scheme regardless of which hue was chosen.

export const ACCENTS = {
  violet: '#6C4CD6',
  neutral: '#18181B',
  red: '#BA1A1A',
  pink: '#B3266E',
  indigo: '#445E91',
  blue: '#0061A4',
  cyan: '#006874',
  teal: '#006A6A',
  green: '#386A20',
  orange: '#8B5000',
} as const;

export type AccentName = keyof typeof ACCENTS;

export const ACCENT_NAMES = Object.keys(ACCENTS) as AccentName[];
// Black and white. The accent now drives every surface, not just the buttons,
// so the default has to be the one that imposes least on an instance that
// never opens the picker. The :root fallbacks in chat.css are this accent's
// own ramp — change one and the other has to follow.
export const DEFAULT_ACCENT: AccentName = 'neutral';

export interface AccentPreference {
  accent: AccentName | 'custom';
  customAccent: string;
}

const HEX_RE = /^#[0-9a-fA-F]{6}$/;

export function normalizeHex(color: string | null | undefined, fallback: string): string;
export function normalizeHex(color: string | null | undefined, fallback: null): string | null;
export function normalizeHex(color: string | null | undefined, fallback: string | null): string | null {
  const value = (color ?? '').trim();
  return HEX_RE.test(value) ? value.toUpperCase() : fallback;
}

export function hexToRgba(color: string, alpha: number): string {
  const hex = normalizeHex(color, ACCENTS[DEFAULT_ACCENT]).replace('#', '');
  const r = parseInt(hex.slice(0, 2), 16);
  const g = parseInt(hex.slice(2, 4), 16);
  const b = parseInt(hex.slice(4, 6), 16);
  return `rgba(${r}, ${g}, ${b}, ${alpha})`;
}

interface Hsl {
  h: number;
  s: number;
  l: number;
}

export function hexToHsl(color: string): Hsl {
  const hex = normalizeHex(color, ACCENTS[DEFAULT_ACCENT]).replace('#', '');
  const r = parseInt(hex.slice(0, 2), 16) / 255;
  const g = parseInt(hex.slice(2, 4), 16) / 255;
  const b = parseInt(hex.slice(4, 6), 16) / 255;
  const max = Math.max(r, g, b);
  const min = Math.min(r, g, b);
  const l = (max + min) / 2;
  const d = max - min;
  let h = 0;
  let s = 0;
  if (d !== 0) {
    s = d / (1 - Math.abs(2 * l - 1));
    if (max === r) h = 60 * (((g - b) / d) % 6);
    else if (max === g) h = 60 * ((b - r) / d + 2);
    else h = 60 * ((r - g) / d + 4);
    if (h < 0) h += 360;
  }
  return { h, s: s * 100, l: l * 100 };
}

export function hslToHex(h: number, s: number, l: number): string {
  const sat = s / 100;
  const light = l / 100;
  const c = (1 - Math.abs(2 * light - 1)) * sat;
  const x = c * (1 - Math.abs(((h / 60) % 2) - 1));
  const m = light - c / 2;
  let r = 0;
  let g = 0;
  let b = 0;
  if (h < 60) { r = c; g = x; b = 0; }
  else if (h < 120) { r = x; g = c; b = 0; }
  else if (h < 180) { r = 0; g = c; b = x; }
  else if (h < 240) { r = 0; g = x; b = c; }
  else if (h < 300) { r = x; g = 0; b = c; }
  else { r = c; g = 0; b = x; }
  const toHex = (v: number) => Math.round((v + m) * 255).toString(16).padStart(2, '0');
  return `#${toHex(r)}${toHex(g)}${toHex(b)}`.toUpperCase();
}

// The stored preference to the raw hue the user actually picked, before any
// scheme adjustment. What the picker's own dot shows.
export function baseAccent(pref: AccentPreference): string {
  if (pref.accent === 'custom') return normalizeHex(pref.customAccent, ACCENTS[DEFAULT_ACCENT]);
  return ACCENTS[pref.accent] ?? ACCENTS[DEFAULT_ACCENT];
}

// The hue, adjusted to a lightness that stays readable against this scheme's
// background: pulled down for light mode, pushed up for dark.
export function displayAccent(accentHex: string, isDark: boolean): string {
  const { h, s, l } = hexToHsl(accentHex);
  const targetL = isDark ? Math.max(l, 70) : Math.min(l, 45);
  return hslToHex(h, s, targetL);
}

export function shiftHex(color: string, amount: number): string {
  const hex = normalizeHex(color, ACCENTS[DEFAULT_ACCENT]).replace('#', '');
  const next = [0, 2, 4].map((idx) => {
    const value = Math.max(0, Math.min(255, parseInt(hex.slice(idx, idx + 2), 16) + amount));
    return value.toString(16).padStart(2, '0');
  });
  return `#${next.join('')}`.toUpperCase();
}

// --- the surface ramp ------------------------------------------------------
//
// The violet the standalone build shipped was not one colour with a neutral
// interface around it: every surface, border and text colour in that palette
// is the same hue at a fixed saturation and lightness. Read the default
// tokens back as HSL and they all land within a few degrees of #6C4CD6.
//
// That is why picking red used to change the buttons and leave the interface
// violet — the ramp was baked into the stylesheet, and only --ai-primary was
// ever recomputed. These tables are that ramp, measured off the original
// values, so violet reproduces itself exactly and every other hue gets the
// treatment violet always had.

interface RampStop {
  token: string;
  s: number;
  l: number;
}

const LIGHT_RAMP: RampStop[] = [
  { token: '--ai-text', s: 7, l: 11.4 },
  { token: '--ai-text-secondary', s: 5, l: 37 },
  { token: '--ai-surface-container', s: 45, l: 95.9 },
  { token: '--ai-surface-container-low', s: 42, l: 97.3 },
  { token: '--ai-surface-container-high', s: 40, l: 93.5 },
  // Deeper than any of those: a field carries no outline, so its own fill is
  // the only thing that says where it starts.
  { token: '--ai-field-bg', s: 44, l: 91.5 },
  { token: '--ai-field-bg-hover', s: 44, l: 88.5 },
];

const DARK_RAMP: RampStop[] = [
  { token: '--ai-text', s: 39, l: 93.5 },
  { token: '--ai-text-secondary', s: 12, l: 69.6 },
  { token: '--ai-surface-card', s: 19, l: 10.2 },
  { token: '--ai-surface-container', s: 16, l: 14.5 },
  { token: '--ai-surface-container-low', s: 17, l: 11.4 },
  { token: '--ai-surface-container-high', s: 15, l: 18.4 },
  { token: '--ai-field-bg', s: 16, l: 21 },
  { token: '--ai-field-bg-hover', s: 16, l: 25 },
];

/**
 * Every themed token for one hue and one scheme, as CSS custom properties.
 *
 * The hue comes from the accent the user picked; the saturation is scaled by
 * how saturated that pick actually was, so a near-grey custom colour gives a
 * near-grey interface instead of a fully tinted one in an arbitrary hue.
 */
export function accentPalette(accentHex: string, isDark: boolean): Record<string, string> {
  const { h, s } = hexToHsl(accentHex);
  // 40% is roughly the least saturated preset; at or above it the ramp runs
  // at full strength, below it the whole interface desaturates with the pick.
  const strength = Math.min(1, s / 40);

  const palette: Record<string, string> = {};
  for (const stop of (isDark ? DARK_RAMP : LIGHT_RAMP)) {
    palette[stop.token] = hslToHex(h, stop.s * strength, stop.l);
  }
  // A light scheme's card stays paper white; tinting it as well leaves no
  // surface for the tinted ones to read against.
  if (!isDark) palette['--ai-surface-card'] = '#FFFFFF';

  // Borders were always the text colour at low alpha, in both schemes.
  const text = palette['--ai-text'] ?? (isDark ? '#ECE8F5' : '#1C1B1F');
  palette['--ai-border'] = hexToRgba(text, 0.12);
  palette['--ai-outline-variant'] = hexToRgba(text, 0.16);
  return palette;
}

// Perceived-brightness threshold (ITU-R BT.601 luma), used to decide whether
// text painted on top of a colour should be black or white.
export function colorIsLight(color: string): boolean {
  const hex = normalizeHex(color, '#000000').replace('#', '');
  const r = parseInt(hex.slice(0, 2), 16);
  const g = parseInt(hex.slice(2, 4), 16);
  const b = parseInt(hex.slice(4, 6), 16);
  return (r * 299 + g * 587 + b * 114) / 1000 >= 150;
}
