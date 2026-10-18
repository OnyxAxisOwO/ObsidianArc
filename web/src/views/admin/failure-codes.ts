// The backoffice's refusals, by code, in the reader's language.
//
// Here and not in lib/api-error.ts, whose table travels on every reader's
// first paint: these sentences are only ever shown to an administrator, so
// they arrive with the backoffice's chunk, and test/bundle.test.ts holds that
// none of their keys reaches the main bundle. A code that is not in either
// table, a plugin's among them, keeps the server's own sentence.

import { ApiError } from '@/api/client';
import type { AdminKey } from '@/i18n.admin';
import { t } from '@/composables/useI18n';
import { codeText } from '@/lib/api-error';

const ADMIN_CODE_KEYS: Record<string, AdminKey> = {
  cannot_ban_self: 'cannotBanSelf',
  cannot_delete_self: 'cannotDeleteSelf',
  account_not_found: 'adminErrAccountNotFound',
  account_required: 'adminErrAccountRequired',
  account_status_invalid: 'adminErrAccountStatusInvalid',
  api_key_required: 'adminErrApiKeyRequired',
  api_key_too_long: 'adminErrApiKeyTooLong',
  api_restriction_choice_required: 'adminErrApiRestrictionChoiceRequired',
  api_restriction_hours_range: 'adminErrApiRestrictionHoursRange',
  application_not_found: 'adminErrApplicationNotFound',
  application_public_no_secret: 'adminErrApplicationPublicNoSecret',
  audit_entry_not_found: 'adminErrAuditEntryNotFound',
  auth_card_position_unknown: 'adminErrAuthCardPositionUnknown',
  authenticator_name_invalid: 'adminErrAuthenticatorNameInvalid',
  backoffice_verification_unknown: 'adminErrBackofficeVerificationUnknown',
  backup_not_configured: 'adminErrBackupNotConfigured',
  ban_reason_too_long: 'adminErrBanReasonTooLong',
  bonus_adjust_invalid: 'adminErrBonusAdjustInvalid',
  bonus_target_required: 'adminErrBonusTargetRequired',
  catalogue_file_empty: 'adminErrCatalogueFileEmpty',
  cleanup_time_invalid: 'adminErrCleanupTimeInvalid',
  conversation_not_found: 'adminErrConversationNotFound',
  days_negative: 'adminErrDaysNegative',
  dimension_unknown: 'adminErrDimensionUnknown',
  feedback_kind_unknown: 'adminErrFeedbackKindUnknown',
  feedback_priority_unknown: 'adminErrFeedbackPriorityUnknown',
  feedback_status_unknown: 'adminErrFeedbackStatusUnknown',
  group_name_length: 'adminErrGroupNameLength',
  group_not_found: 'adminErrGroupNotFound',
  group_required: 'adminErrGroupRequired',
  health_hours_range: 'adminErrHealthHoursRange',
  image_base64_invalid: 'adminErrImageBase64Invalid',
  image_too_large: 'adminErrImageTooLarge',
  invite_batch_code: 'adminErrInviteBatchCode',
  invite_count_range: 'adminErrInviteCountRange',
  invite_group_days_needs_group: 'adminErrInviteGroupDaysNeedsGroup',
  invite_group_days_range: 'adminErrInviteGroupDaysRange',
  invite_max_uses_range: 'adminErrInviteMaxUsesRange',
  invite_not_found: 'adminErrInviteNotFound',
  key_not_found: 'adminErrKeyNotFound',
  key_rotation_invalid: 'adminErrKeyRotationInvalid',
  landing_mode_unknown: 'adminErrLandingModeUnknown',
  leaderboard_identity_unknown: 'adminErrLeaderboardIdentityUnknown',
  login_background_type: 'adminErrLoginBackgroundType',
  login_background_variant_unknown: 'adminErrLoginBackgroundVariantUnknown',
  logo_type: 'adminErrLogoType',
  mail_recipient_required: 'adminErrMailRecipientRequired',
  malformed_action: 'adminErrMalformedAction',
  malformed_actor_id: 'adminErrMalformedActorId',
  malformed_filter_id: 'adminErrMalformedFilterId',
  malformed_group_id: 'adminErrMalformedGroupId',
  malformed_identifier: 'adminErrMalformedIdentifier',
  malformed_model_id: 'adminErrMalformedModelId',
  malformed_provider_id: 'adminErrMalformedProviderId',
  malformed_session_id: 'adminErrMalformedSessionId',
  malformed_target: 'adminErrMalformedTarget',
  malformed_tz: 'adminErrMalformedTz',
  malformed_user_id: 'adminErrMalformedUserId',
  member_ids_invalid: 'adminErrMemberIdsInvalid',
  member_selection_size: 'adminErrMemberSelectionSize',
  membership_expiry_invalid: 'adminErrMembershipExpiryInvalid',
  membership_expiry_needs_group: 'adminErrMembershipExpiryNeedsGroup',
  name_required: 'adminErrNameRequired',
  notice_body_too_long: 'adminErrNoticeBodyTooLong',
  notice_title_too_long: 'adminErrNoticeTitleTooLong',
  notice_tone_unknown: 'adminErrNoticeToneUnknown',
  order_duplicate_model: 'adminErrOrderDuplicateModel',
  outcome_filter_invalid: 'adminErrOutcomeFilterInvalid',
  page_empty: 'adminErrPageEmpty',
  page_too_large: 'adminErrPageTooLarge',
  permission_unknown: 'adminErrPermissionUnknown',
  policy_scope_invalid: 'adminErrPolicyScopeInvalid',
  policy_target_required: 'adminErrPolicyTargetRequired',
  provider_kind_invalid: 'adminErrProviderKindInvalid',
  provider_name_length: 'adminErrProviderNameLength',
  provider_not_found: 'adminErrProviderNotFound',
  provider_required: 'adminErrProviderRequired',
  pwa_icon_invalid: 'adminErrPwaIconInvalid',
  reasoning_style_unknown: 'adminErrReasoningStyleUnknown',
  request_cancelled: 'adminErrRequestCancelled',
  retention_days_range: 'adminErrRetentionDaysRange',
  role_filter_unknown: 'adminErrRoleFilterUnknown',
  role_invalid: 'adminErrRoleInvalid',
  route_chained: 'adminErrRouteChained',
  route_to_self: 'adminErrRouteToSelf',
  rpm_negative: 'adminErrRpmNegative',
  session_not_found: 'adminErrSessionNotFound',
  setting_accent_invalid: 'adminErrSettingAccentInvalid',
  setting_colour_invalid: 'adminErrSettingColourInvalid',
  setting_out_of_range: 'adminErrSettingOutOfRange',
  setting_too_long: 'adminErrSettingTooLong',
  setting_unknown: 'adminErrSettingUnknown',
  settings_file_empty: 'adminErrSettingsFileEmpty',
  smtp_not_configured: 'adminErrSmtpNotConfigured',
  state_filter_unknown: 'adminErrStateFilterUnknown',
  status_code_invalid: 'adminErrStatusCodeInvalid',
  status_filter_unknown: 'adminErrStatusFilterUnknown',
  theme_mode_unknown: 'adminErrThemeModeUnknown',
  timestamp_invalid: 'adminErrTimestampInvalid',
  too_many_api_keys: 'adminErrTooManyApiKeys',
  too_many_headers: 'adminErrTooManyHeaders',
  tpm_negative: 'adminErrTpmNegative',
  two_factor_policy_unknown: 'adminErrTwoFactorPolicyUnknown',
  usage_display_unknown: 'adminErrUsageDisplayUnknown',
  usage_reset_scope: 'adminErrUsageResetScope',
  usercheck_exempt_domain: 'adminErrUsercheckExemptDomain',
  usercheck_failure_mode: 'adminErrUsercheckFailureMode',
  usercheck_key_missing: 'adminErrUsercheckKeyMissing',
  usercheck_key_refused: 'adminErrUsercheckKeyRefused',
  usercheck_key_required: 'adminErrUsercheckKeyRequired',
  usercheck_key_unreadable: 'adminErrUsercheckKeyUnreadable',
  usercheck_settings_invalid: 'adminErrUsercheckSettingsInvalid',
  usercheck_test_address_required: 'adminErrUsercheckTestAddressRequired',
  usercheck_unavailable: 'adminErrUsercheckUnavailable',
  window_credit_limit: 'adminErrWindowCreditLimit',
  window_request_limit: 'adminErrWindowRequestLimit',
  window_token_limit: 'adminErrWindowTokenLimit',
  window_unknown: 'adminErrWindowUnknown',
};

/**
 * The sentence for a refusal code, or undefined when neither this table nor
 * the reader's own has one. The values a sentence quotes come from the
 * answer's details; only plain values are passed on, so a nested detail
 * cannot reach a sentence as text.
 */
export function adminCodeText(code: string, details: Record<string, unknown> = {}): string | undefined {
  const key = Object.hasOwn(ADMIN_CODE_KEYS, code) ? ADMIN_CODE_KEYS[code] : undefined;
  if (!key) return codeText(code, details);
  const vars: Record<string, string | number> = {};
  for (const [name, value] of Object.entries(details)) {
    if (typeof value === 'string' || typeof value === 'number') vars[name] = value;
  }
  return t(key, vars);
}

/**
 * A refusal's sentence by its code, else the server's own sentence; anything
 * that is not a refusal is shown as String() shows it, as these screens
 * always have.
 */
export function refusalLine(failure: unknown): string {
  if (failure instanceof ApiError) return adminCodeText(failure.code, failure.details) ?? failure.message;
  return String(failure);
}

/**
 * refusalLine for the screens that show a thrown Error by its message alone
 * rather than with its name in front.
 */
export function errorLine(failure: unknown): string {
  if (failure instanceof ApiError) return adminCodeText(failure.code, failure.details) ?? failure.message;
  if (failure instanceof Error) return failure.message;
  return String(failure);
}
