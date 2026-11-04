import { ApiError } from '@/api/client';
import { t, type StringKey } from '@/composables/useI18n';

/**
 * A failure whose sentence was written for logs rather than for a reader: a
 * server error, the 'internal' code, or no answer at all. One rule, shared by
 * every helper that turns a failure into a line, so no two of them disagree
 * about which sentences a reader may see.
 */
export function serverFailure(failure: ApiError): boolean {
  return failure.status === 0 || failure.status >= 500 || failure.code === 'internal';
}

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
  if (serverFailure(failure)) return t(fallback);
  return failure.message;
}
