// How an account is named and pictured.

import type { Account } from '@/api/auth';

export function displayName(account: Account): string {
  return account.nickname || account.username;
}

/**
 * An avatar is stored as free text, so it is checked before it becomes an
 * `img` src: a same-origin path or an inline image, nothing that would turn
 * every page view into a request to somewhere else.
 */
export function safeAvatar(value: string): string | null {
  const trimmed = value.trim();
  if (!trimmed) return null;
  if (trimmed.startsWith('/') && !trimmed.startsWith('//')) return trimmed;
  if (/^data:image\/(?:png|jpeg|webp|gif|avif);base64,[A-Za-z0-9+/]+=*$/.test(trimmed)) return trimmed;
  return null;
}

export function initials(name: string): string {
  const trimmed = name.trim();
  if (!trimmed) return '?';
  // Works for a single word and for "First Last"; for CJK, the first
  // character is already the recognisable one.
  const parts = trimmed.split(/\s+/);
  if (parts.length === 1) return [...trimmed][0] ?? '?';
  return `${[...(parts[0] ?? '')][0] ?? ''}${[...(parts[parts.length - 1] ?? '')][0] ?? ''}`;
}
