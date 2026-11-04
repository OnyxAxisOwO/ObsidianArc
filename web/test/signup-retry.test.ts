// A sign-up that raced the first account is refused with a code, and the form
// words that code in the reader's language. The server's English sentence is
// only the fallback for a code nobody has translated.

import { afterEach, describe, expect, it } from 'vitest';
import { ApiError } from '@/api/client';
import { changeLanguage, t } from '@/composables/useI18n';
import { refusalText } from '@/lib/refusal';

const SERVER_SENTENCE = 'Another account was being opened at the same moment.';

afterEach(async () => {
  await changeLanguage('en');
});

describe('sign-up sent back to be made again', () => {
  it('words the retry in the reader language', async () => {
    await changeLanguage('zh');

    const text = refusalText(new ApiError(409, 'signup_retry', SERVER_SENTENCE));

    expect(text).toBe(t('signupRetry'));
    expect(text).not.toBe(SERVER_SENTENCE);
  });
});
