import { beforeEach, describe, expect, it } from 'vitest';
import { changeLanguage, t } from '../src/composables/useI18n';
import { eventLabel, formatEventReason, reviewDecision } from '../src/views/admin/security-text';

beforeEach(async () => {
  await changeLanguage('en');
});

// A reason is stored as the text the server wrote, so the table has to know
// every wording a build has written: the reviewer's English from before it
// spoke Chinese, and the Chinese it writes now.
describe('the words for a stored reason', () => {
  it('translates the reviewer reasons in both the old and the current wording', () => {
    expect(formatEventReason('no reviewer configured')).toBe(t('securityReasonNoReviewer'));
    expect(formatEventReason('未配置审查模型')).toBe(t('securityReasonNoReviewer'));
    expect(formatEventReason('审查模型不可用')).toBe(t('securityReasonModelUnavailable'));
    expect(formatEventReason('审查请求失败')).toBe(t('securityReasonReviewFailed'));
    expect(formatEventReason('模型返回内容无法解析')).toBe(t('securityReasonUnparseableAnswer'));
  });

  // The sentence internal/auth/signup_proof.go writes when the difficulty was
  // raised under a challenge already handed out, full-width comma and all.
  it('translates the raised proof-of-work tier', () => {
    expect(formatEventReason('PoW 难度已提高，挑战需重新获取')).toBe(t('securityReasonPoWTierRaised'));
  });

  it('keeps the count in a timed API restriction', () => {
    expect(formatEventReason('administrator restricted API access for 12 hours'))
      .toBe(t('securityReasonAPIHours', { count: 12 }));
  });

  it('shows what it has no words for as it was written', () => {
    expect(formatEventReason('名字看起来像批量注册')).toBe('名字看起来像批量注册');
    expect(formatEventReason('constructor')).toBe('constructor');
    expect(formatEventReason('')).toBe('');
  });
});

describe('decisions and event types', () => {
  it('words the known ones and passes the rest through', () => {
    expect(reviewDecision('restrict')).toBe(t('securityDecisionRestrict'));
    expect(reviewDecision('exit_0')).toBe('exit_0');
    expect(eventLabel('log_prune')).toBe(t('securityEventLogPrune'));
    expect(eventLabel('toString')).toBe('toString');
  });
});
