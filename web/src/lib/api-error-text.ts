import { ApiError } from '@/api/client';
import { t, type StringKey } from '@/composables/useI18n';

/**
 * The line a person reads when a request fails.
 *
 * A failure the screen has a code for is translated. A server failure, or no
 * answer at all, gets the screen's own fallback: the server's sentence for it
 * is English and was written for logs and API clients, and a Chinese screen
 * must not show it. Any other refusal keeps the server's wording, as it did
 * before codes were added, until that refusal is given one.
 */
export function apiFailureText(
  failure: unknown,
  fallback: StringKey,
  codes: Partial<Record<string, StringKey>> = {},
): string {
  if (!(failure instanceof ApiError)) return t(fallback);
  // The code is looked up before the status is judged, so a coded failure is
  // translated whatever its status, as the header above promises.
  const key = Object.hasOwn(codes, failure.code) ? codes[failure.code] : undefined;
  if (key) return t(key);
  if (failure.status === 0 || failure.status >= 500 || failure.code === 'internal') return t(fallback);
  return failure.message;
}
