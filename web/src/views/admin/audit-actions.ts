import type { AuditTextKey } from './audit-text';

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
export const AUDIT_ACTIONS: Record<string, AuditTextKey> = {
  'POST /api/admin/health/probe': 'doHealthProbe',
  'POST /api/admin/health/reset': 'doHealthReset',
  'POST /api/admin/security/review': 'doSecurityReview',
  'PUT /api/admin/mail': 'doMailSave',
  'POST /api/admin/mail/test': 'doMailTest',
  'PUT /api/admin/usercheck': 'doUserCheckSave',
  'POST /api/admin/usercheck/test': 'doUserCheckTest',
  'POST /api/admin/applications': 'doAppCreate',
  'PATCH /api/admin/applications/{id}': 'doAppUpdate',
  'POST /api/admin/applications/{id}/secret': 'doAppSecret',
  'DELETE /api/admin/applications/{id}': 'doAppDelete',
  'POST /api/admin/users': 'doUserCreate',
  'PATCH /api/admin/users/{id}': 'doUserUpdate',
  'DELETE /api/admin/users/{id}': 'doUserDelete',
  'POST /api/admin/users/{id}/password': 'doUserPassword',
  'DELETE /api/admin/users/{id}/two-factor': 'doUserTwoFactor',
  'DELETE /api/admin/users/{id}/keys/{key}': 'doUserKeyRevoke',
  'DELETE /api/admin/users/{id}/sessions/{sid}': 'doUserSessionRevoke',
  'DELETE /api/admin/users/{id}/sessions': 'doUserSessionsRevoke',
  'GET /api/admin/users/{id}/conversations': 'doUserConversations',
  'GET /api/admin/users/{id}/conversations/{conversation}': 'doUserTranscript',
  'POST /api/admin/users/{id}/cards': 'doCardGrant',
  'PATCH /api/admin/users/{id}/cards': 'doCardReschedule',
  'DELETE /api/admin/users/{id}/cards/{card}': 'doCardRevoke',
  'POST /api/admin/groups': 'doGroupCreate',
  'PATCH /api/admin/groups/{id}': 'doGroupUpdate',
  'POST /api/admin/groups/{id}/members': 'doGroupMembers',
  'DELETE /api/admin/groups/{id}': 'doGroupDelete',
  'PUT /api/admin/settings': 'doSettingsSave',
  'POST /api/admin/settings/import': 'doSettingsImport',
  'POST /api/admin/attachments/purge': 'doAttachmentsPurge',
  'PUT /api/admin/login-background/{variant}': 'doBackgroundSave',
  'DELETE /api/admin/login-background/{variant}': 'doBackgroundDelete',
  'PUT /api/admin/logo': 'doLogoSave',
  'DELETE /api/admin/logo': 'doLogoDelete',
  'POST /api/admin/providers': 'doProviderCreate',
  'PATCH /api/admin/providers/{id}': 'doProviderUpdate',
  'DELETE /api/admin/providers/{id}': 'doProviderDelete',
  'POST /api/admin/providers/{id}/detect': 'doProviderDetect',
  'POST /api/admin/models': 'doModelCreate',
  'PATCH /api/admin/models/{id}': 'doModelUpdate',
  'DELETE /api/admin/models/{id}': 'doModelDelete',
  'PUT /api/admin/models/order': 'doModelOrder',
  'POST /api/admin/models/import': 'doModelImport',
  'POST /api/admin/logs/prune': 'doLogsPrune',
  'POST /api/admin/usage/reset': 'doUsageReset',
  'PUT /api/admin/checkin': 'doCheckinSave',
  'POST /api/admin/bonus/bars': 'doBonusCreate',
  'PUT /api/admin/bonus/bars/{id}': 'doBonusUpdate',
  'DELETE /api/admin/bonus/bars/{id}': 'doBonusDelete',
  'POST /api/admin/bonus/bars/{id}/grants': 'doBonusGrant',
  'DELETE /api/admin/bonus/grants/{id}': 'doBonusRevoke',
  'POST /api/admin/bonus/bars/{id}/pool': 'doBonusPool',
  'POST /api/admin/codes': 'doCodeCreate',
  'DELETE /api/admin/codes/{id}': 'doCodeDelete',
  'PUT /api/admin/quota/policies': 'doPolicySave',
  'DELETE /api/admin/quota/policies/{scope}': 'doPolicyDelete',
  'POST /api/admin/announcements': 'doAnnouncementCreate',
  'PATCH /api/admin/announcements/{id}': 'doAnnouncementUpdate',
  'DELETE /api/admin/announcements/{id}': 'doAnnouncementDelete',
  'PATCH /api/admin/feedback/{id}': 'doFeedbackUpdate',
  'DELETE /api/admin/feedback/{id}': 'doFeedbackDelete',
  'POST /api/admin/feedback/{id}/replies': 'doFeedbackReply',
  'DELETE /api/admin/feedback/{id}/replies/{reply}': 'doFeedbackReplyDelete',
  'POST /api/admin/invites': 'doInviteCreate',
  'DELETE /api/admin/invites/{id}': 'doInviteRevoke',
  'PUT /api/admin/backup': 'doBackupSave',
  'POST /api/admin/backup/test': 'doBackupTest',
  'POST /api/admin/backup/run': 'doBackupRun',
  'POST /api/admin/plugins/preview': 'doPluginPreview',
  'POST /api/admin/plugins/install-package': 'doPluginUpload',
  'POST /api/admin/plugins/{name}/install': 'doPluginInstall',
  'POST /api/admin/plugins/{name}/enable': 'doPluginEnable',
  'POST /api/admin/plugins/{name}/disable': 'doPluginDisable',
  'POST /api/admin/plugins/{name}/uninstall': 'doPluginUninstall',
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
