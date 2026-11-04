import { describe, expect, it } from 'vitest';
import { ApiError } from '@/api/client';
import { t } from '@/composables/useI18n';
import { challengeText, refusalText } from '@/lib/refusal';

// Every screen that asks for a human check or a proof of work reads these words
// from one place, so a rate limit, an expired proof and a failed check cannot be
// folded into one sentence on one screen and kept apart on another.
describe('challenge wording', () => {
  it.each([
    ['challenge_failed', 'challengeFailed'],
    ['challenge_unavailable', 'challengeUnavailable'],
    ['pow_required', 'powRequired'],
    ['pow_expired', 'powExpired'],
    ['pow_rate_limited', 'powRateLimited'],
    ['pow_invalid_signature', 'powVerificationFailed'],
    ['pow_max_exceeded', 'powVerificationFailed'],
    ['pow_invalid_nonce', 'powVerificationFailed'],
    ['pow_replayed', 'powVerificationFailed'],
  ] as const)('words %s as its own sentence', (code, key) => {
    expect(challengeText(new ApiError(400, code, 'server English'))).toBe(t(key));
  });

  it('keeps a rate limit apart from an expired proof', () => {
    const limited = challengeText(new ApiError(429, 'pow_rate_limited', ''));
    const expired = challengeText(new ApiError(400, 'pow_expired', ''));
    expect(limited).not.toBe(expired);
  });

  it('has no sentence for a failure that is not a challenge', () => {
    expect(challengeText(new ApiError(400, 'email_required', ''))).toBeUndefined();
    expect(challengeText(new Error('network down'))).toBeUndefined();
    expect(challengeText('challenge_failed')).toBeUndefined();
  });

  it('is what the sign-in refusal says for the same codes', () => {
    expect(refusalText(new ApiError(429, 'pow_rate_limited', ''))).toBe(t('powRateLimited'));
    expect(refusalText(new ApiError(400, 'challenge_failed', ''))).toBe(t('challengeFailed'));
  });
});
