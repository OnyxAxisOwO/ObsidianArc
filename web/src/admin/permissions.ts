import type { StringKey } from '@/i18n';

export const ADMIN_PERMISSIONS: Array<{ value: string; label: StringKey }> = [
  { value: 'dashboard', label: 'navDashboard' },
  { value: 'groups', label: 'navGroups' },
  { value: 'users', label: 'navUsers' },
  { value: 'providers', label: 'navProviders' },
  { value: 'models', label: 'navModels' },
  { value: 'availability', label: 'navAvailability' },
  { value: 'leaderboard', label: 'navLeaderboard' },
  { value: 'usage', label: 'navUsage' },
  { value: 'resources', label: 'navResources' },
  { value: 'codes', label: 'navCodes' },
  { value: 'invites', label: 'permInvites' },
  { value: 'logs', label: 'navLogs' },
  { value: 'security', label: 'navSecurity' },
  { value: 'settings', label: 'navSettings' },
  { value: 'announcements', label: 'announcements' },
  { value: 'feedback', label: 'navFeedback' },
  { value: 'administrators', label: 'manageAdministrators' },
];
