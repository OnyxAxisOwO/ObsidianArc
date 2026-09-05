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
export const DEFAULT_ACCENT: AccentName = 'violet';

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

// Perceived-brightness threshold (ITU-R BT.601 luma), used to decide whether
// text painted on top of a colour should be black or white.
export function colorIsLight(color: string): boolean {
  const hex = normalizeHex(color, '#000000').replace('#', '');
  const r = parseInt(hex.slice(0, 2), 16);
  const g = parseInt(hex.slice(2, 4), 16);
  const b = parseInt(hex.slice(4, 6), 16);
  return (r * 299 + g * 587 + b * 114) / 1000 >= 150;
}
