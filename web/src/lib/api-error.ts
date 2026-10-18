// The text to show a reader for a failed request.
//
// The server answers with a stable code and an English sentence. The code is
// what gets translated; the sentence is only the fallback for a code nobody has
// written a reader's version of, so a Chinese screen is not left showing
// English where the code alone already says what happened.
//
// A server failure (a 5xx, the 'internal' code, or no answer at all) is the one
// case where the sentence is not shown even without a translation: it was
// written for logs and API clients. The reader gets the screen's generic line.
// The code is still looked up first, because provider outages arrive as 5xx
// responses whose codes have their own reader's sentence.

import { ApiError } from '@/api/client';
import { t, type StringKey } from '@/composables/useI18n';
import { serverFailure } from '@/lib/api-error-text';
import { absoluteTime } from '@/lib/format';

const CODE_KEYS: Record<string, StringKey> = {
  too_many_pending_images: 'errTooManyPendingImages',
  attachment_quota_full: 'errAttachmentQuotaFull',
  storage_full: 'errStorageFull',
  // Export and backup refusals a reader can act on. Without a sentence here they
  // would show the server's English, which is written for logs.
  export_too_large: 'errExportTooLarge',
  backup_in_progress: 'errBackupInProgress',
  backup_busy: 'errBackupBusy',
  // The trial screen already has these two sentences, so the chat uses the same
  // wording rather than a second, drifting copy.
  trial_exhausted: 'trialExhausted',
  trial_finished: 'trialEndedSignUp',
  probe_cooldown: 'errProbeCooldown',
  email_unverified: 'errEmailUnverified',
  current_session: 'errCurrentSession',
  bonus_bar_off: 'errBonusBarOff',
  bonus_no_account: 'errBonusNoAccount',
  bonus_models_unreadable: 'errBonusModelsUnreadable',
  preferences_too_large: 'errPreferencesTooLarge',
  attachment_unavailable: 'errAttachmentUnavailable',
  reward_unavailable: 'errRewardUnavailable',
  // The check-in screen's refusals. They were shown as the server's English
  // whenever a screen did not map them itself.
  already_checked_in: 'errAlreadyCheckedIn',
  milestone_not_reached: 'errMilestoneNotReached',
  milestone_claimed: 'errMilestoneClaimed',
  checkin_disabled: 'errCheckinDisabled',
  card_batch_too_large: 'errCardBatchTooLarge',
  provider_rate_limited: 'errProviderRateLimited',
  provider_unreachable: 'errProviderUnreachable',
  provider_error: 'errProviderError',
  provider_auth: 'errProviderAuth',
  bonus_bar_in_checkin: 'errBonusBarInCheckin',
  provider_disabled: 'errProviderDisabled',
  model_routed: 'errModelRouted',
  plugin_timeout: 'errPluginTimeout',
  // The import limits quote their figures from the answer's details, as
  // export_too_large above does.
  import_rate_limited: 'errImportRateLimited',
  import_wrong_format: 'errImportWrongFormat',
  import_too_large: 'errImportTooLarge',
  import_nul_character: 'errImportNULCharacter',
  feedback_daily_limit: 'feedbackNoneLeft',
  feedback_not_found: 'errFeedbackNotFound',
  feedback_title_invalid: 'errFeedbackTitleInvalid',
  feedback_body_invalid: 'errFeedbackBodyInvalid',
  feedback_kind_invalid: 'errFeedbackKindInvalid',
  feedback_priority_invalid: 'errFeedbackPriorityInvalid',
  feedback_status_invalid: 'errFeedbackStatusInvalid',
  feedback_thread_full: 'errFeedbackThreadFull',
  announcement_id_required: 'errAnnouncementIDRequired',
  announcement_not_found: 'errAnnouncementNotFound',
  announcement_title_invalid: 'errAnnouncementTitleInvalid',
  announcement_too_long: 'errAnnouncementTooLong',
  announcement_mode_invalid: 'errAnnouncementModeInvalid',
  // A connection that never reached the server is already worded by the client
  // in the reader's language. The status rule below would replace that with the
  // generic sentence, which says less about what went wrong.
  network: 'connectionFailed',
  // A failed request can be the one that signs the tab out, and its flash can
  // land after the redirect to sign-in, so the reader gets the sentence here
  // rather than the server's English.
  session_required: 'errSessionEnded',
  // images_unsupported, refusal and provider_rejected have no entry on purpose:
  // their server sentence names what the provider or model said, and a shared
  // translation would erase that. chat.Describe documents the same choice.
};

// Which window tripped is the part a reader can act on, so each window has its
// own sentence. The keys are the server's own window names.
const QUOTA_KEYS: Record<string, StringKey> = {
  rpm: 'errQuotaRPM',
  tpm: 'errQuotaTPM',
  '5h': 'errQuota5H',
  '1w': 'errQuotaWeek',
  '1m': 'errQuotaMonth',
};

// The reader's sentence for a code the interface has one for, or undefined. A
// stream's error event carries the same code, so it is translated through here
// too rather than shown as the server's English.
export function codeText(code: string, details: Record<string, unknown> = {}): string | undefined {
  // The index is typed as possibly undefined under noUncheckedIndexedAccess,
  // so the lookup is narrowed before it is handed to t().
  const key = Object.hasOwn(CODE_KEYS, code) ? CODE_KEYS[code] : undefined;
  return key ? t(key, sentenceVars(details)) : undefined;
}

// The figures a reader's sentence quotes, taken from the answer's details. Only
// plain values are passed on, so a nested detail cannot reach a sentence as text.
function sentenceVars(details: Record<string, unknown>): Record<string, string | number> {
  const vars: Record<string, string | number> = {};
  for (const [name, value] of Object.entries(details)) {
    if (typeof value === 'string' || typeof value === 'number') vars[name] = value;
  }
  return vars;
}

// The sentence for a failed turn as the conversation stored it. The row keeps
// the server's code beside its English sentence, so the reader's language is
// chosen by the code, as it is for a live error. A row with no code was written
// before the code was kept, and shows the sentence it was written with. An
// 'internal' failure is one the server never meant a reader to see, so it gets
// the screen's generic line, the same as apiErrorText gives.
export function storedErrorText(code: string, sentence: string): string {
  if (code === 'internal') return t('failed');
  return codeText(code) ?? sentence;
}

// `fallback` is the screen's own line for a failure it has no better words for,
// shown where the server's sentence must not be (a server fault, a failure that
// was not an API answer at all). Without one, the generic line is used.
export function apiErrorText(error: unknown, fallback: StringKey = 'failed'): string {
  if (!(error instanceof ApiError)) {
    // A network or engine failure reads as `TypeError: Failed to fetch`, which
    // is for whoever reads the console, not the reader. The original goes to
    // the console so it is not lost.
    console.warn('request failed', error);
    return t(fallback);
  }
  if (error.code === 'quota_exceeded') {
    // The window name comes from the server, so a value like "constructor" must
    // not find a prototype member and be handed to t().
    const windowName = String(error.details['window']);
    return t((Object.hasOwn(QUOTA_KEYS, windowName) ? QUOTA_KEYS[windowName] : undefined) ?? 'errQuotaGeneric');
  }
  if (error.code === 'pool_exhausted') {
    // Which limit it was, and when it lifts, is what the reader can act on.
    const text = t(error.details['reason'] === 'daily_limit' ? 'errPoolDailyLimit' : 'errPoolEmpty');
    const resets = Number(error.details['resets_at']) || 0;
    return resets > 0 ? `${text} ${t('quotaResets', { when: absoluteTime(resets) })}` : text;
  }
  const text = codeText(error.code, error.details);
  if (text) return text;
  // A server failure's sentence is English and was written for logs and API
  // clients, so a reader on a Chinese screen gets the screen's own fallback.
  if (serverFailure(error)) return t(fallback);
  return error.message;
}
