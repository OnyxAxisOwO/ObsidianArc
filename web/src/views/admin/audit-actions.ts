import type { StringKey } from '@/composables/useI18n';

/**
 * What each backoffice action is called on the audit trail, keyed by method
 * and route pattern exactly as the server records them. Keys rather than
 * words, resolved at render, because this table is evaluated before the
 * language is known.
 *
 * A route missing here is shown as its method and path: a plugin's routes,
 * and any core route added since, still read as something rather than as
 * nothing.
 */
export const AUDIT_ACTIONS: Record<string, StringKey> = {
  'POST /api/admin/health/probe': 'auditDoHealthProbe',
  'POST /api/admin/health/reset': 'auditDoHealthReset',
  'POST /api/admin/security/review': 'auditDoSecurityReview',
  'PUT /api/admin/mail': 'auditDoMailSave',
  'POST /api/admin/mail/test': 'auditDoMailTest',
  'PUT /api/admin/usercheck': 'auditDoUserCheckSave',
  'POST /api/admin/usercheck/test': 'auditDoUserCheckTest',
  'POST /api/admin/applications': 'auditDoAppCreate',
  'PATCH /api/admin/applications/{id}': 'auditDoAppUpdate',
  'POST /api/admin/applications/{id}/secret': 'auditDoAppSecret',
  'DELETE /api/admin/applications/{id}': 'auditDoAppDelete',
  'POST /api/admin/users': 'auditDoUserCreate',
  'PATCH /api/admin/users/{id}': 'auditDoUserUpdate',
  'DELETE /api/admin/users/{id}': 'auditDoUserDelete',
  'POST /api/admin/users/{id}/password': 'auditDoUserPassword',
  'DELETE /api/admin/users/{id}/two-factor': 'auditDoUserTwoFactor',
  'DELETE /api/admin/users/{id}/keys/{key}': 'auditDoUserKeyRevoke',
  'DELETE /api/admin/users/{id}/sessions/{sid}': 'auditDoUserSessionRevoke',
  'DELETE /api/admin/users/{id}/sessions': 'auditDoUserSessionsRevoke',
  'GET /api/admin/users/{id}/conversations': 'auditDoUserConversations',
  'GET /api/admin/users/{id}/conversations/{conversation}': 'auditDoUserTranscript',
  'POST /api/admin/users/{id}/cards': 'auditDoCardGrant',
  'PATCH /api/admin/users/{id}/cards': 'auditDoCardReschedule',
  'DELETE /api/admin/users/{id}/cards/{card}': 'auditDoCardRevoke',
  'POST /api/admin/groups': 'auditDoGroupCreate',
  'PATCH /api/admin/groups/{id}': 'auditDoGroupUpdate',
  'POST /api/admin/groups/{id}/members': 'auditDoGroupMembers',
  'DELETE /api/admin/groups/{id}': 'auditDoGroupDelete',
  'PUT /api/admin/settings': 'auditDoSettingsSave',
  'POST /api/admin/settings/import': 'auditDoSettingsImport',
  'POST /api/admin/attachments/purge': 'auditDoAttachmentsPurge',
  'PUT /api/admin/login-background/{variant}': 'auditDoBackgroundSave',
  'DELETE /api/admin/login-background/{variant}': 'auditDoBackgroundDelete',
  'PUT /api/admin/logo': 'auditDoLogoSave',
  'DELETE /api/admin/logo': 'auditDoLogoDelete',
  'POST /api/admin/providers': 'auditDoProviderCreate',
  'PATCH /api/admin/providers/{id}': 'auditDoProviderUpdate',
  'DELETE /api/admin/providers/{id}': 'auditDoProviderDelete',
  'POST /api/admin/providers/{id}/detect': 'auditDoProviderDetect',
  'POST /api/admin/models': 'auditDoModelCreate',
  'PATCH /api/admin/models/{id}': 'auditDoModelUpdate',
  'DELETE /api/admin/models/{id}': 'auditDoModelDelete',
  'PUT /api/admin/models/order': 'auditDoModelOrder',
  'POST /api/admin/models/import': 'auditDoModelImport',
  'POST /api/admin/logs/prune': 'auditDoLogsPrune',
  'POST /api/admin/usage/reset': 'auditDoUsageReset',
  'PUT /api/admin/checkin': 'auditDoCheckinSave',
  'POST /api/admin/bonus/bars': 'auditDoBonusCreate',
  'PUT /api/admin/bonus/bars/{id}': 'auditDoBonusUpdate',
  'DELETE /api/admin/bonus/bars/{id}': 'auditDoBonusDelete',
  'POST /api/admin/bonus/bars/{id}/grants': 'auditDoBonusGrant',
  'DELETE /api/admin/bonus/grants/{id}': 'auditDoBonusRevoke',
  'POST /api/admin/bonus/bars/{id}/pool': 'auditDoBonusPool',
  'POST /api/admin/codes': 'auditDoCodeCreate',
  'DELETE /api/admin/codes/{id}': 'auditDoCodeDelete',
  'PUT /api/admin/quota/policies': 'auditDoPolicySave',
  'DELETE /api/admin/quota/policies/{scope}': 'auditDoPolicyDelete',
  'POST /api/admin/announcements': 'auditDoAnnouncementCreate',
  'PATCH /api/admin/announcements/{id}': 'auditDoAnnouncementUpdate',
  'DELETE /api/admin/announcements/{id}': 'auditDoAnnouncementDelete',
  'PATCH /api/admin/feedback/{id}': 'auditDoFeedbackUpdate',
  'DELETE /api/admin/feedback/{id}': 'auditDoFeedbackDelete',
  'POST /api/admin/feedback/{id}/replies': 'auditDoFeedbackReply',
  'DELETE /api/admin/feedback/{id}/replies/{reply}': 'auditDoFeedbackReplyDelete',
  'POST /api/admin/invites': 'auditDoInviteCreate',
  'DELETE /api/admin/invites/{id}': 'auditDoInviteRevoke',
  'PUT /api/admin/backup': 'auditDoBackupSave',
  'POST /api/admin/backup/test': 'auditDoBackupTest',
  'POST /api/admin/backup/run': 'auditDoBackupRun',
  'POST /api/admin/plugins/preview': 'auditDoPluginPreview',
  'POST /api/admin/plugins/install-package': 'auditDoPluginUpload',
  'POST /api/admin/plugins/{name}/install': 'auditDoPluginInstall',
  'POST /api/admin/plugins/{name}/enable': 'auditDoPluginEnable',
  'POST /api/admin/plugins/{name}/disable': 'auditDoPluginDisable',
  'POST /api/admin/plugins/{name}/uninstall': 'auditDoPluginUninstall',
};

/**
 * How serious an action is, for the mark down the row's edge: a removal or a
 * credential reset is something to see while scrolling, an edit is not.
 */
export function auditWeight(method: string, route: string): 'normal' | 'notable' {
  if (method === 'DELETE') return 'notable';
  if (method === 'GET') return 'notable';
  return /\/(password|secret|uninstall|purge|prune|reset|import)$/.test(route) ? 'notable' : 'normal';
}
