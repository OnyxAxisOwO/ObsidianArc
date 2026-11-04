// The number and time formats the tables are read in.
//
// Lifted out of the table component so that nothing importing one of these
// four-line helpers drags a component and its stylesheet in with it. That is
// not hypothetical: one import of `formatUptime` from the About panel used to
// pull the whole backoffice back into the main bundle.

import { currentLanguage, t } from '@/composables/useI18n';

/**
 * The locale every date and time is printed in: the interface's language, not
 * the browser's. Without this a reader who picks Chinese on an English browser
 * gets English dates inside a Chinese sentence.
 */
export function localeTag(): string {
  return currentLanguage() === 'zh' ? 'zh-CN' : 'en-US';
}

/**
 * A token figure the provider never reported, marked as the estimate it is.
 * The mark goes in front of the number rather than in a column of its own:
 * a whole column that is almost always empty is noise, and "≈" is read before
 * the digits it qualifies.
 */
export function tokenFigure(value: number, estimated?: boolean): string {
  return estimated ? `≈${compactNumber(value)}` : compactNumber(value);
}

/** 12345 → "12.3k". Tables are read at a glance, not audited in. */
export function compactNumber(value: number): string {
  if (!Number.isFinite(value)) return '—';
  // Decided on the figure as it prints: 999.996 rounds to 1000 at two decimals,
  // which no unit can hold, so it falls through and reads as 1k.
  const rounded = Math.round(value * 100) / 100;
  if (Math.abs(rounded) < 1000) {
    return Number.isInteger(value) ? String(value) : String(rounded);
  }
  // Decided on the figure as it prints: 999950 rounds to 1000.0k, which is 1M.
  if (Math.abs(value) < 1_000_000) {
    const thousands = Math.round(value / 100) / 10;
    if (Math.abs(thousands) < 1000) return `${thousands}k`;
  }
  // The same rule one unit up: 999950000 rounds to 1000.0M, which is 1B.
  const millions = Math.round(value / 100_000) / 10;
  if (Math.abs(millions) < 1000) return `${millions}M`;
  return `${Math.round(value / 100_000_000) / 10}B`;
}

// Constructing a formatter is the expensive part of printing a date, and the
// usage board prints every row again each time it refreshes, every 15 seconds.
const dayFormatters = new Map<string, Intl.DateTimeFormat>();

function dayFormatter(tag: string): Intl.DateTimeFormat {
  let formatter = dayFormatters.get(tag);
  if (!formatter) {
    formatter = new Intl.DateTimeFormat(tag);
    dayFormatters.set(tag, formatter);
  }
  return formatter;
}

export function relativeTime(at: number): string {
  if (!at) return '—';
  const seconds = Math.round((Date.now() - at) / 1000);
  // Every phrase below is in the past tense, so a moment that has not
  // happened yet came out as "just now" — a card expiring in a month read as
  // one expiring this second. There is no future vocabulary here to reach
  // for, and inventing one is a set of strings this has never needed, so a
  // future moment is given as the date it is.
  if (seconds < 0) return new Date(at).toLocaleString(localeTag());
  if (seconds < 60) return t('timeJustNow');
  const minutes = Math.round(seconds / 60);
  if (minutes < 60) return t('timeMinutes', { count: minutes });
  const hours = Math.round(minutes / 60);
  if (hours < 48) return t('timeHours', { count: hours });
  const days = Math.round(hours / 24);
  if (days < 30) return t('timeDays', { count: days });
  return dayFormatter(localeTag()).format(new Date(at));
}

export function absoluteTime(at: number): string {
  if (!at) return '—';
  return new Date(at).toLocaleString(localeTag());
}

export function formatUptime(seconds: number): string {
  if (seconds < 60) return `${seconds}s`;
  // Each unit is taken only when its rounded figure is below the next unit's
  // threshold, so 3570 seconds reads as 1h rather than 60m.
  const minutes = Math.round(seconds / 60);
  if (minutes < 60) return `${minutes}m`;
  const hours = Math.round(seconds / 3600);
  if (hours < 24) return `${hours}h`;
  return `${Math.round(seconds / 86400)}d`;
}

/** Bytes as a figure a person reads, to one decimal below a gigabyte. */
export function formatBytes(count: number): string {
  const mb = count / (1024 * 1024);
  // Decided on the rounded figure, as the megabyte line prints it: 1023.6 MB
  // would otherwise read as "1024 MB".
  if (Math.round(mb) >= 1024) return `${(mb / 1024).toFixed(1)} GB`;
  if (mb >= 10) return `${Math.round(mb)} MB`;
  if (mb >= 0.1) return `${mb.toFixed(1)} MB`;
  if (count >= 1024) return `${Math.round(count / 1024)} KB`;
  return `${count} B`;
}
