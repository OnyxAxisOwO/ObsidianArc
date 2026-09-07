// The number and time formats the tables are read in.
//
// Lifted out of the table component so that nothing importing one of these
// four-line helpers drags a component and its stylesheet in with it. That is
// not hypothetical: one import of `formatUptime` from the About panel used to
// pull the whole backoffice back into the main bundle.

import { t } from '@/composables/useI18n';

/** 12345 → "12.3k". Tables are read at a glance, not audited in. */
export function compactNumber(value: number): string {
  if (!Number.isFinite(value)) return '—';
  if (Math.abs(value) < 1000) {
    return Number.isInteger(value) ? String(value) : String(Math.round(value * 100) / 100);
  }
  if (Math.abs(value) < 1_000_000) return `${Math.round(value / 100) / 10}k`;
  return `${Math.round(value / 100_000) / 10}M`;
}

export function relativeTime(at: number): string {
  if (!at) return '—';
  const seconds = Math.round((Date.now() - at) / 1000);
  // Every phrase below is in the past tense, so a moment that has not
  // happened yet came out as "just now" — a card expiring in a month read as
  // one expiring this second. There is no future vocabulary here to reach
  // for, and inventing one is a set of strings this has never needed, so a
  // future moment is given as the date it is.
  if (seconds < 0) return new Date(at).toLocaleString();
  if (seconds < 60) return t('timeJustNow');
  const minutes = Math.round(seconds / 60);
  if (minutes < 60) return t('timeMinutes', { count: minutes });
  const hours = Math.round(minutes / 60);
  if (hours < 48) return t('timeHours', { count: hours });
  const days = Math.round(hours / 24);
  if (days < 30) return t('timeDays', { count: days });
  return new Date(at).toLocaleDateString();
}

export function absoluteTime(at: number): string {
  if (!at) return '—';
  return new Date(at).toLocaleString();
}

export function formatUptime(seconds: number): string {
  if (seconds < 60) return `${seconds}s`;
  if (seconds < 3600) return `${Math.round(seconds / 60)}m`;
  if (seconds < 86400) return `${Math.round(seconds / 3600)}h`;
  return `${Math.round(seconds / 86400)}d`;
}

/** Bytes as a figure a person reads, to one decimal below a gigabyte. */
export function formatBytes(count: number): string {
  const mb = count / (1024 * 1024);
  if (mb >= 1024) return `${(mb / 1024).toFixed(1)} GB`;
  if (mb >= 10) return `${Math.round(mb)} MB`;
  if (mb >= 0.1) return `${mb.toFixed(1)} MB`;
  if (count >= 1024) return `${Math.round(count / 1024)} KB`;
  return `${count} B`;
}
