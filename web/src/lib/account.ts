// How an account is named and pictured.

import type { Account } from '@/api/auth';
import { safeNext } from '@/lib/next';

export function displayName(account: Account): string {
  return account.nickname || account.username;
}

/**
 * An avatar is stored as free text, so it is checked before it becomes an
 * `img` src: a same-origin path or an inline image, nothing that would turn
 * every page view into a request to somewhere else. The two supported avatar
 * services are the only external exceptions, with bounded image parameters.
 */
export function safeAvatar(value: string): string | null {
  const trimmed = value.trim();
  if (!trimmed) return null;
  // The rule a sign-in destination is held to. A browser reads a backslash as
  // a slash and drops a tab or newline, so "/\host" or "/<TAB>/host" would
  // fetch from another site, and this is the check that catches both.
  const path = safeNext(trimmed);
  if (path) return path;
  if (/^data:image\/(?:png|jpeg|webp|gif|avif);base64,[A-Za-z0-9+/]+=*$/.test(trimmed)) return trimmed;
  if (/^https:\/\/(?:gravatar\.com|www\.gravatar\.com|[012]\.gravatar\.com|weavatar\.com)\/avatar\/(?:[a-f0-9]{32}|[a-f0-9]{64})(?:\.(?:png|jpg|jpeg|webp|gif|avif))?(?:\?[^#]*)?$/i.test(trimmed)) {
    const url = new URL(trimmed);
    for (const [key, value] of url.searchParams) {
      if ((key === 's' || key === 'size') && /^\d{1,4}$/.test(value) && Number(value) >= 1 && Number(value) <= 2000) continue;
      if ((key === 'd' || key === 'default') && /^(?:404|mp|identicon|monsterid|wavatar|retro|robohash|blank)$/.test(value)) continue;
      if ((key === 'r' || key === 'rating') && /^(?:g|pg|r|x)$/.test(value)) continue;
      if ((key === 'f' || key === 'forcedefault') && value === 'y') continue;
      return null;
    }
    return trimmed;
  }
  return null;
}

// Both services understand the same email hash. Keep the backup lazy: only an
// image error advances to it, so a successful first choice costs one request.
export function avatarSources(value: string): string[] {
  const source = safeAvatar(value);
  if (!source) return [];
  if (!source.startsWith('https://')) return [source];
  const backup = new URL(source);
  backup.hostname = backup.hostname === 'weavatar.com' ? 'gravatar.com' : 'weavatar.com';
  return [source, backup.href];
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
