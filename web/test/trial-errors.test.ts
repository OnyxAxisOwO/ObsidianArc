// The trial's server answers in English. A Chinese visitor who ran out of
// questions used to read the server's sentence in the transcript, because the
// client passed it through untranslated.

import { afterEach, describe, expect, it, vi } from 'vitest';
import { changeLanguage, t } from '@/composables/useI18n';
import { ApiError } from '@/api/client';
import { streamTrial, trialErrorMessage } from '@/api/trial';

const englishExhausted = 'You have used the trial for now. Create an account to keep going.';

afterEach(async () => {
  vi.unstubAllGlobals();
  await changeLanguage('en');
});

describe('trial error text', () => {
  it('translates the codes the trial answers with', async () => {
    await changeLanguage('zh');

    expect(trialErrorMessage('trial_exhausted', englishExhausted)).toBe(t('trialExhausted'));
    expect(trialErrorMessage('trial_busy', 'x')).toBe(t('trialBusy'));
    expect(trialErrorMessage('trial_finished', 'x')).toBe(t('trialEndedSignUp'));
    expect(trialErrorMessage('upstream', 'x')).toBe(t('trialFailed'));
    expect(trialErrorMessage('trial_exhausted', englishExhausted)).not.toContain('You have used');
  });

  it('translates the refusals that used to share the generic bad_request code', async () => {
    await changeLanguage('zh');

    expect(trialErrorMessage('trial_empty', 'Nothing to send.')).toBe(t('trialNothingToSend'));
    expect(trialErrorMessage('trial_empty', 'Nothing to send.')).not.toBe('Nothing to send.');
    expect(trialErrorMessage('trial_too_long', 'x')).toBe(t('trialTooLong'));
    expect(trialErrorMessage('trial_unavailable', 'x')).toBe(t('trialUnavailable'));

    const longMessage = 'Message must be 4000 characters or fewer.';
    expect(trialErrorMessage('trial_message_too_long', longMessage)).toBe(
      t('trialMessageTooLong', { max: 4000 }),
    );
    expect(trialErrorMessage('trial_message_too_long', longMessage)).toContain('4000');
    expect(trialErrorMessage('trial_message_too_long', longMessage)).not.toContain('Message must');
  });

  it('keeps the server text for a code it does not know', () => {
    expect(trialErrorMessage('bad_request', 'Nothing to send.')).toBe('Nothing to send.');
  });

  it('keeps the server text when the length limit is missing from its sentence', async () => {
    await changeLanguage('zh');

    expect(trialErrorMessage('trial_message_too_long', 'Too long.')).toBe('Too long.');
  });

  it('shows the translated line when the request is refused', async () => {
    await changeLanguage('zh');
    vi.stubGlobal('fetch', vi.fn(async () => new Response(
      JSON.stringify({ error: { code: 'trial_exhausted', message: englishExhausted } }),
      { status: 429, headers: { 'Content-Type': 'application/json' } },
    )));

    const error = await streamTrial([{ role: 'user', content: 'hello' }], '', () => {}).catch(
      (reason: unknown) => reason,
    );

    expect(error).toBeInstanceOf(ApiError);
    expect((error as ApiError).code).toBe('trial_exhausted');
    expect((error as ApiError).message).toBe(t('trialExhausted'));
  });

  it('shows the translated line when the stream reports a busy trial', async () => {
    await changeLanguage('zh');
    const frame = 'event: error\ndata: {"code":"trial_busy","message":"The trial is busy right now."}\n\n';
    vi.stubGlobal('fetch', vi.fn(async () => new Response(frame, {
      status: 200,
      headers: { 'Content-Type': 'text/event-stream' },
    })));

    const error = await streamTrial([{ role: 'user', content: 'hello' }], '', () => {}).catch(
      (reason: unknown) => reason,
    );

    expect(error).toBeInstanceOf(ApiError);
    expect((error as ApiError).code).toBe('trial_busy');
    expect((error as ApiError).message).toBe(t('trialBusy'));
  });
});
