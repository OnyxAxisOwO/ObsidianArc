// The refusal that signs a tab out can be the one whose flash lands after the
// redirect to sign-in, so it is worded by its code in the reader's language
// rather than shown as the server's English sentence.

import { afterEach, expect, it } from 'vitest';
import { ApiError } from '../src/api/client';
import { changeLanguage, t } from '../src/composables/useI18n';
import { apiErrorText } from '../src/lib/api-error';

afterEach(async () => {
  await changeLanguage('en');
});

it('words a session_required refusal in the reader\'s language', async () => {
  await changeLanguage('zh');
  const text = apiErrorText(new ApiError(401, 'session_required', 'Sign in to continue.'));

  expect(text).toBe(t('errSessionEnded'));
  expect(text).not.toBe('Sign in to continue.');
});
