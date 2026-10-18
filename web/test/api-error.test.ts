// A coded refusal is shown in the reader's language, worded by its code. The
// server's English sentence is only kept for a code nobody has translated.

import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import { ApiError } from '@/api/client';
import { changeLanguage, t } from '@/composables/useI18n';
import { absoluteTime } from '@/lib/format';
import { apiErrorText, codeText, storedErrorText } from '@/lib/api-error';

const SERVER_SENTENCE = 'The server said this in English.';

const CODES = [
  'too_many_pending_images',
  'attachment_quota_full',
  'storage_full',
  'trial_exhausted',
  'trial_finished',
  'probe_cooldown',
  'email_unverified',
  'current_session',
  'attachment_unavailable',
  'reward_unavailable',
  'provider_rate_limited',
  'provider_unreachable',
  'provider_error',
  'provider_auth',
  'session_required',
  'bonus_bar_in_checkin',
  'provider_disabled',
  'model_routed',
  'plugin_timeout',
  'network',
];

beforeEach(async () => {
  await changeLanguage('en');
});

afterEach(async () => {
  await changeLanguage('en');
});

describe('apiErrorText', () => {
  it.each(CODES)('translates %s into the reader\'s language', async (code) => {
    const error = new ApiError(429, code, SERVER_SENTENCE);

    await changeLanguage('zh');
    const zh = apiErrorText(error);
    await changeLanguage('en');
    const en = apiErrorText(error);

    expect(zh).not.toBe(SERVER_SENTENCE);
    expect(en).not.toBe(SERVER_SENTENCE);
    expect(zh).not.toBe(en);
  });

  it('words a quota refusal by the window that tripped', async () => {
    await changeLanguage('zh');
    const error = new ApiError(429, 'quota_exceeded', SERVER_SENTENCE, { window: '5h' });

    expect(apiErrorText(error)).toBe(t('errQuota5H'));
    expect(apiErrorText(new ApiError(429, 'quota_exceeded', SERVER_SENTENCE, { window: 'rpm' })))
      .toBe(t('errQuotaRPM'));
  });

  it('falls back to a generic quota line when the window is unknown', async () => {
    await changeLanguage('zh');

    expect(apiErrorText(new ApiError(429, 'quota_exceeded', SERVER_SENTENCE)))
      .toBe(t('errQuotaGeneric'));
  });

  it('words an empty pool apart from a spent daily share, with when it lifts', async () => {
    await changeLanguage('zh');
    expect(apiErrorText(new ApiError(429, 'pool_exhausted', SERVER_SENTENCE, { reason: 'empty', resets_at: 0 })))
      .toBe(t('errPoolEmpty'));
    const when = Date.UTC(2031, 0, 2);
    expect(apiErrorText(new ApiError(429, 'pool_exhausted', SERVER_SENTENCE, { reason: 'daily_limit', resets_at: when })))
      .toBe(`${t('errPoolDailyLimit')} ${t('quotaResets', { when: absoluteTime(when) })}`);
  });

  it('keeps the server sentence for a code nobody has translated', () => {
    expect(apiErrorText(new ApiError(400, 'some_new_code', SERVER_SENTENCE))).toBe(SERVER_SENTENCE);
  });

  // The server's English for a 5xx was written for logs; a Chinese screen must
  // show the screen's own line for it, the same rule apiFailureText applies.
  it('shows the screen fallback for a server failure with no translation', async () => {
    await changeLanguage('zh');

    expect(apiErrorText(new ApiError(500, 'some_new_code', SERVER_SENTENCE))).toBe(t('failed'));
    expect(apiErrorText(new ApiError(503, 'some_new_code', SERVER_SENTENCE))).toBe(t('failed'));
    expect(apiErrorText(new ApiError(400, 'internal', SERVER_SENTENCE))).toBe(t('failed'));
  });

  // A screen with a more specific generic line than 'failed' passes its own,
  // and it replaces only what 'failed' would have: a coded or 4xx answer keeps
  // its own wording.
  it('uses the screen\'s own fallback where it names one', async () => {
    await changeLanguage('zh');
    const warn = vi.spyOn(console, 'warn').mockImplementation(() => {});

    expect(apiErrorText(new ApiError(502, 'bad_gateway', SERVER_SENTENCE), 'usageUnavailable'))
      .toBe(t('usageUnavailable'));
    expect(apiErrorText(new TypeError('Failed to fetch'), 'usageUnavailable')).toBe(t('usageUnavailable'));
    expect(apiErrorText(new ApiError(401, 'session_required', SERVER_SENTENCE), 'usageUnavailable'))
      .toBe(t('errSessionEnded'));
    expect(apiErrorText(new ApiError(400, 'some_new_code', SERVER_SENTENCE), 'usageUnavailable'))
      .toBe(SERVER_SENTENCE);
    warn.mockRestore();
  });

  // The server-failure rule must come after the code lookup, or a provider
  // outage (a 5xx with a code) would lose its translation to the generic line.
  it('translates a coded server failure rather than showing the generic line', async () => {
    await changeLanguage('zh');

    expect(apiErrorText(new ApiError(502, 'provider_unreachable', SERVER_SENTENCE)))
      .toBe(t('errProviderUnreachable'));
    expect(apiErrorText(new ApiError(500, 'provider_error', SERVER_SENTENCE)))
      .toBe(t('errProviderError'));
  });

  it('keeps the client\'s own connection sentence when the server was never reached', async () => {
    await changeLanguage('zh');

    expect(apiErrorText(new ApiError(0, 'network', t('connectionFailed')))).toBe(t('connectionFailed'));
  });

  // The trial screen and the chat share these two codes, so they share the words.
  it('words a trial refusal with the trial screen\'s sentence', async () => {
    await changeLanguage('zh');

    expect(apiErrorText(new ApiError(429, 'trial_exhausted', SERVER_SENTENCE))).toBe(t('trialExhausted'));
    expect(apiErrorText(new ApiError(409, 'trial_finished', SERVER_SENTENCE))).toBe(t('trialEndedSignUp'));
  });

  // The window comes from the server. A prototype name must not be looked up
  // as if it were a quota window.
  it.each(['constructor', 'toString', '__proto__'])('treats the window %s as unknown', async (windowName) => {
    await changeLanguage('zh');
    const error = new ApiError(429, 'quota_exceeded', SERVER_SENTENCE, { window: windowName });

    expect(apiErrorText(error)).toBe(t('errQuotaGeneric'));
  });

  // A stream's error event has no ApiError to go through, so it is translated by
  // its code alone. A code without a translation, or a prototype key, must not
  // resolve to something the interface never wrote.
  it('translates a stream error code, and reports none for one without a translation', async () => {
    await changeLanguage('zh');

    expect(codeText('attachment_unavailable')).toBe(t('errAttachmentUnavailable'));
    expect(codeText('internal')).toBeUndefined();
    expect(codeText('constructor')).toBeUndefined();
  });

  it('shows a non-API failure as the generic sentence in the reader\'s language, and logs the original', async () => {
    const warn = vi.spyOn(console, 'warn').mockImplementation(() => {});
    const failure = new TypeError('Failed to fetch');

    await changeLanguage('zh');
    const zh = apiErrorText(failure);
    expect(zh).toBe(t('failed'));
    await changeLanguage('en');
    const en = apiErrorText(failure);
    expect(en).toBe(t('failed'));

    expect(zh).not.toBe(en);
    expect(zh).not.toContain('Failed to fetch');
    expect(en).not.toContain('Failed to fetch');
    expect(warn).toHaveBeenCalledWith('request failed', failure);
    warn.mockRestore();
  });
});

// A failed turn is stored with its code beside the English sentence. The bubble
// is redrawn from that row on every reopening, so the code has to decide the
// language there as it does for a live error.
describe('storedErrorText', () => {
  it('shows a stored failure in the reader\'s language by its code', async () => {
    await changeLanguage('zh');
    const zh = storedErrorText('provider_rate_limited', SERVER_SENTENCE);
    expect(zh).toBe(t('errProviderRateLimited'));
    expect(zh).not.toBe(SERVER_SENTENCE);
    await changeLanguage('en');
    expect(storedErrorText('provider_rate_limited', SERVER_SENTENCE)).toBe(t('errProviderRateLimited'));
  });

  it('shows the screen fallback for a stored internal failure', async () => {
    await changeLanguage('zh');

    expect(storedErrorText('internal', SERVER_SENTENCE)).toBe(t('failed'));
  });

  // A row written before the code was kept has none, so it shows the sentence it
  // was written with, as it always did.
  it('keeps the stored sentence for a row with no code', async () => {
    await changeLanguage('zh');

    expect(storedErrorText('', SERVER_SENTENCE)).toBe(SERVER_SENTENCE);
  });

  it('keeps the stored sentence for a code nobody has translated', async () => {
    await changeLanguage('zh');

    expect(storedErrorText('images_unsupported', SERVER_SENTENCE)).toBe(SERVER_SENTENCE);
  });
});
