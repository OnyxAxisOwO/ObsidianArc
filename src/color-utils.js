// The accent-color picker's color math, adapted from PageDye's
// scripts/shared/color-utils.js (the sibling project this was extracted
// from) so the two keep the same "pick one hue, get a scheme that works in
// both light and dark" trick.
//
// A user picks ONE hex per accent — there is no separate light/dark color to
// manage — and getDisplayAccentColor clamps that hue's lightness per scheme
// before it is ever painted: light mode pulls it down to a readable-on-white
// shade, dark mode pushes it up to a readable-on-near-black one. That is
// what makes --ai-primary-text safe to leave as a fixed white/near-black per
// scheme (see chat.css) regardless of which hue the user chose.

(function (root, factory) {
  const api = factory();
  if (typeof module === 'object' && module.exports) module.exports = api;
  if (root) root.ObsidianColorUtils = api;
})(typeof globalThis !== 'undefined' ? globalThis : this, function () {
  'use strict';

  // "violet" is Obsidian Arc's own default; the rest are PageDye's named
  // accents verbatim, so a hex picked here means the same thing there.
  const ACCENTS = Object.freeze({
    violet: '#6C4CD6',
    neutral: '#18181B',
    red: '#BA1A1A',
    pink: '#B3266E',
    indigo: '#445E91',
    blue: '#0061A4',
    cyan: '#006874',
    teal: '#006A6A',
    green: '#386A20',
    orange: '#8B5000'
  });
  const ACCENT_NAMES = Object.freeze(Object.keys(ACCENTS));
  const DEFAULT_ACCENT = 'violet';

  const HEX_RE = /^#[0-9a-fA-F]{6}$/;

  function normalizeHexColor(color, fallback) {
    const value = (color || '').trim();
    return HEX_RE.test(value) ? value.toUpperCase() : fallback;
  }

  function hexToRgba(color, alpha) {
    const hex = normalizeHexColor(color, ACCENTS[DEFAULT_ACCENT]).replace('#', '');
    const r = parseInt(hex.slice(0, 2), 16);
    const g = parseInt(hex.slice(2, 4), 16);
    const b = parseInt(hex.slice(4, 6), 16);
    return `rgba(${r}, ${g}, ${b}, ${alpha})`;
  }

  function hexToHsl(color) {
    const hex = normalizeHexColor(color, ACCENTS[DEFAULT_ACCENT]).replace('#', '');
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
      switch (max) {
        case r: h = 60 * (((g - b) / d) % 6); break;
        case g: h = 60 * ((b - r) / d + 2); break;
        default: h = 60 * ((r - g) / d + 4); break;
      }
      if (h < 0) h += 360;
    }
    return { h, s: s * 100, l: l * 100 };
  }

  function hslToHex(h, s, l) {
    const sat = s / 100;
    const light = l / 100;
    const c = (1 - Math.abs(2 * light - 1)) * sat;
    const x = c * (1 - Math.abs(((h / 60) % 2) - 1));
    const m = light - c / 2;
    let r = 0, g = 0, b = 0;
    if (h < 60) { r = c; g = x; b = 0; }
    else if (h < 120) { r = x; g = c; b = 0; }
    else if (h < 180) { r = 0; g = c; b = x; }
    else if (h < 240) { r = 0; g = x; b = c; }
    else if (h < 300) { r = x; g = 0; b = c; }
    else { r = c; g = 0; b = x; }
    const toHex = (v) => Math.round((v + m) * 255).toString(16).padStart(2, '0');
    return ('#' + toHex(r) + toHex(g) + toHex(b)).toUpperCase();
  }

  // The stored preference -> the raw hue the user actually picked, before any
  // scheme adjustment. What the picker's own swatch/dot shows.
  function baseAccentColor(pref) {
    const source = pref && typeof pref === 'object' ? pref : {};
    if (source.accent === 'custom') return normalizeHexColor(source.customAccent, ACCENTS[DEFAULT_ACCENT]);
    return ACCENTS[source.accent] || ACCENTS[DEFAULT_ACCENT];
  }

  // The hue, adjusted to a lightness that stays readable against this
  // scheme's background: pulled down for light mode, pushed up for dark.
  function getDisplayAccentColor(accentHex, isDark) {
    const { h, s, l } = hexToHsl(accentHex);
    const targetL = isDark ? Math.max(l, 70) : Math.min(l, 45);
    return hslToHex(h, s, targetL);
  }

  function shiftHexColor(color, amount) {
    const hex = normalizeHexColor(color, ACCENTS[DEFAULT_ACCENT]).replace('#', '');
    const next = [0, 2, 4].map((idx) => {
      const value = Math.max(0, Math.min(255, parseInt(hex.slice(idx, idx + 2), 16) + amount));
      return value.toString(16).padStart(2, '0');
    });
    return '#' + next.join('').toUpperCase();
  }

  // Perceived-brightness threshold (ITU-R BT.601 luma), used to decide
  // whether text/an icon painted on top of a color should be black or white.
  function colorIsLight(color) {
    const hex = normalizeHexColor(color, '#000000').replace('#', '');
    const r = parseInt(hex.slice(0, 2), 16);
    const g = parseInt(hex.slice(2, 4), 16);
    const b = parseInt(hex.slice(4, 6), 16);
    return (r * 299 + g * 587 + b * 114) / 1000 >= 150;
  }

  return {
    ACCENTS,
    ACCENT_NAMES,
    DEFAULT_ACCENT,
    normalizeHexColor,
    hexToRgba,
    hexToHsl,
    hslToHex,
    shiftHexColor,
    colorIsLight,
    baseAccentColor,
    getDisplayAccentColor
  };
});
