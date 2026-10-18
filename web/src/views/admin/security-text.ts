// The words for what the security log records.
//
// Tables of StringKeys rather than strings, resolved at the call, so they are
// read in whatever language is current when the log draws (see i18n.ts).
// Shared by the log and by the review trial, which reports a reason in the
// same words the log would.

import { t, type StringKey } from '@/composables/useI18n';
import { pluginSecurityEvent, pluginSecurityReason } from '@/plugins/registry';

// A reason is stored as the text the server wrote at the time, in a TEXT
// column, so a row keeps the wording of the build that wrote it. Both the
// English the reviewer used to write and the Chinese it writes now stay here:
// dropping the old ones would turn every older row back into raw text.
const REASONS: Record<string, StringKey> = {
  'administrator lifted the API restriction': 'securityReasonAPILifted',
  'administrator restricted API access permanently': 'securityReasonAPIPermanent',
  'chat submission speed crossed the configured threshold': 'securityReasonChatSpeed',
  'no reviewer configured': 'securityReasonNoReviewer',
  '未配置审查模型': 'securityReasonNoReviewer',
  'model unavailable': 'securityReasonModelUnavailable',
  '审查模型不可用': 'securityReasonModelUnavailable',
  'review failed': 'securityReasonReviewFailed',
  '审查请求失败': 'securityReasonReviewFailed',
  'unparseable answer': 'securityReasonUnparseableAnswer',
  '模型返回内容无法解析': 'securityReasonUnparseableAnswer',
  'review returned no decision': 'securityReasonNoDecision',
  '缺少 PoW 解答': 'securityReasonPoWMissing',
  'PoW 挑战已过期': 'securityReasonPoWExpired',
  'PoW 签名无效': 'securityReasonPoWInvalidSignature',
  'PoW 步数超出上限': 'securityReasonPoWMaxExceeded',
  'PoW 计算结果不匹配': 'securityReasonPoWInvalidNonce',
  'PoW 挑战已被使用': 'securityReasonPoWReplayed',
  'PoW 校验失败': 'securityReasonPoWFailed',
  'Turnstile 人机验证未通过': 'securityReasonTurnstileFailed',
};

const DECISIONS: Record<string, StringKey> = {
  allow: 'securityDecisionAllow',
  refuse: 'securityDecisionRefuse',
  required: 'securityDecisionRequired',
  passed: 'securityDecisionPassed',
  failed: 'securityDecisionFailed',
  enabled: 'securityDecisionEnabled',
  disabled: 'securityDecisionDisabled',
  reset: 'securityDecisionReset',
  recovery_used: 'securityDecisionRecoveryUsed',
  recovery_regenerated: 'securityDecisionRecoveryRegenerated',
  prune: 'securityDecisionPrune',
  pruned: 'securityDecisionPruned',
  restrict: 'securityDecisionRestrict',
};

const EVENTS: Record<string, StringKey> = {
  signup_review: 'securityEventSignupReview',
  api_restriction: 'securityEventAPIRestriction',
  api_restriction_lifted: 'securityEventAPIRestrictionLifted',
  chat_challenge: 'securityEventChatChallenge',
  two_factor: 'securityEventTwoFactor',
  new_device: 'securityEventNewDevice',
  console_command: 'securityEventConsoleCommand',
  plugin: 'securityEventPlugin',
  pow_challenge: 'securityEventPoWChallenge',
  turnstile_challenge: 'securityEventTurnstileChallenge',
  password_reset: 'securityEventPasswordReset',
  account_deleted: 'securityEventAccountDeleted',
  log_prune: 'securityEventLogPrune',
};

// Object.hasOwn rather than a bare lookup: a reason is free text, and one that
// happens to read "constructor" must not find Object.prototype's.
function lookup(table: Record<string, StringKey>, value: string): StringKey | undefined {
  return Object.hasOwn(table, value) ? table[value] : undefined;
}

export function formatEventReason(reason: string): string {
  if (!reason) return '';
  const known = lookup(REASONS, reason);
  if (known) return t(known);
  const hoursMatch = reason.match(/^administrator restricted API access for (\d+) hours$/);
  if (hoursMatch) return t('securityReasonAPIHours', { count: Number(hoursMatch[1]) });
  // What the reviewer model wrote in its own words has no entry and is shown
  // as written; so is anything a newer server says that this build predates.
  return pluginSecurityReason(reason) ?? reason;
}

export function reviewDecision(decision: string): string {
  const known = lookup(DECISIONS, decision);
  // A decision this screen has no words for — a console command's outcome
  // code, say — is shown as it was recorded rather than as a restriction it
  // never was.
  return known ? t(known) : decision;
}

export function eventLabel(event: string): string {
  const known = lookup(EVENTS, event);
  return known ? t(known) : pluginSecurityEvent(event) ?? event;
}
