// The refusals the backup, feedback and announcement handlers answer with a
// code. The reader's screen translates the code, quoting the figures the
// answer carries in its details; the English the server wrote for logs is not
// what a Chinese screen shows.

import { afterEach, beforeEach, describe, expect, it } from 'vitest';
import { ApiError } from '@/api/client';
import { changeLanguage, t } from '@/composables/useI18n';
import { apiErrorText } from '@/lib/api-error';

const SERVER_SENTENCE = 'The server said this in English.';

beforeEach(async () => {
  await changeLanguage('zh');
});

afterEach(async () => {
  await changeLanguage('en');
});

describe('apiErrorText for refusals that quote figures', () => {
  it('quotes the export limits from the details', () => {
    const text = apiErrorText(new ApiError(409, 'export_too_large', SERVER_SENTENCE,
      { conversations: 2000, messages: 50000, mib: 32 }));
    expect(text).toContain('2000');
    expect(text).toContain('50000');
    expect(text).toContain('32 MiB');
    expect(text).not.toContain(SERVER_SENTENCE);
  });

  it('quotes the feedback title length from the details', () => {
    expect(apiErrorText(new ApiError(400, 'feedback_title_invalid', SERVER_SENTENCE, { max: 120 })))
      .toBe(t('errFeedbackTitleInvalid', { max: 120 }));
  });

  it('quotes the announcement title length from the details', () => {
    expect(apiErrorText(new ApiError(400, 'announcement_title_invalid', SERVER_SENTENCE, { max: 120 })))
      .toBe(t('errAnnouncementTitleInvalid', { max: 120 }));
  });
});

describe('apiErrorText for refusals with a fixed sentence', () => {
  it('translates an import refusal that carries no figures', () => {
    expect(apiErrorText(new ApiError(400, 'import_wrong_format', SERVER_SENTENCE)))
      .toBe(t('errImportWrongFormat'));
  });

  it('uses the sentence the reader already has for a feedback day that is used up', () => {
    expect(apiErrorText(new ApiError(429, 'feedback_daily_limit', SERVER_SENTENCE)))
      .toBe(t('feedbackNoneLeft'));
  });

  it('keeps the server sentence for a code the interface does not know', () => {
    expect(apiErrorText(new ApiError(400, 'some_future_code', SERVER_SENTENCE))).toBe(SERVER_SENTENCE);
  });
});
