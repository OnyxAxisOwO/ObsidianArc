// The line for a failed request on a screen that has its own fallback and a
// few codes of its own. The server's English is shown only for a refusal the
// screen has no code for and the server answered as a client error.

import { afterEach, beforeEach, describe, expect, it } from 'vitest';
import { ApiError } from '@/api/client';
import { changeLanguage, t } from '@/composables/useI18n';
import { apiErrorText } from '@/lib/api-error';
import { apiFailureText, serverFailure } from '@/lib/api-error-text';

const SERVER_SENTENCE = 'The server said this in English.';
const CODES = { trial_busy: 'trialBusy' } as const;

beforeEach(async () => {
  await changeLanguage('zh');
});

afterEach(async () => {
  await changeLanguage('en');
});

describe('apiFailureText', () => {
  it('shows the screen fallback when no answer came back at all', () => {
    expect(apiFailureText(new ApiError(0, 'network', SERVER_SENTENCE), 'trialFailed', CODES))
      .toBe(t('trialFailed'));
  });

  it('shows the screen fallback for a server failure, not its English', () => {
    expect(apiFailureText(new ApiError(502, 'bad_gateway', SERVER_SENTENCE), 'trialFailed', CODES))
      .toBe(t('trialFailed'));
  });

  it('translates a coded failure', () => {
    expect(apiFailureText(new ApiError(429, 'trial_busy', SERVER_SENTENCE), 'trialFailed', CODES))
      .toBe(t('trialBusy'));
  });

  it('keeps the server sentence for an unmapped client error', () => {
    expect(apiFailureText(new ApiError(400, 'some_new_code', SERVER_SENTENCE), 'trialFailed', CODES))
      .toBe(SERVER_SENTENCE);
  });

  it('shows the screen fallback for an internal error whatever its status', () => {
    expect(apiFailureText(new ApiError(400, 'internal', SERVER_SENTENCE), 'trialFailed', CODES))
      .toBe(t('trialFailed'));
  });

  it('shows the screen fallback for a value that is not an API error', () => {
    expect(apiFailureText(new TypeError('Failed to fetch'), 'trialFailed', CODES))
      .toBe(t('trialFailed'));
  });
});

// The chat's helper and the screens' helper used to carry the rule each, one
// copied from the other; they share it now and must keep agreeing.
describe('serverFailure', () => {
  it.each([
    [0, 'no_answer', true],
    [500, 'boom', true],
    [503, 'upstream', true],
    [400, 'internal', true],
    [400, 'bad_request', false],
    [404, 'not_found', false],
  ])('status %i with code %s is a server failure: %s', (status, code, expected) => {
    const failure = new ApiError(status, code, SERVER_SENTENCE);
    expect(serverFailure(failure)).toBe(expected);
    expect(apiFailureText(failure, 'failed') === t('failed')).toBe(expected);
    expect(apiErrorText(failure) === t('failed')).toBe(expected);
  });
});
