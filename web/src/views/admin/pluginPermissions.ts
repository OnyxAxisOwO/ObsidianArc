// What each permission a plugin package can ask for means, in the dictionary's
// words, and which ones hand over the keys to everything. The order is the
// order a dialog lists them in. The ids are the server's (arcx.Perm*).

import type { StringKey } from '@/composables/useI18n';

export interface PermissionSpec {
  id: string;
  label: StringKey;
  /** Reaches everything the server has, or acts as the person granting it. */
  risky: boolean;
}

export const PERMISSIONS: PermissionSpec[] = [
  { id: 'db', label: 'pluginPermDb', risky: true },
  { id: 'network', label: 'pluginPermNetwork', risky: true },
  { id: 'users', label: 'pluginPermUsers', risky: true },
  { id: 'console', label: 'pluginPermConsole', risky: true },
  { id: 'cards', label: 'pluginPermCards', risky: false },
  { id: 'sessions', label: 'pluginPermSessions', risky: false },
  { id: 'notify', label: 'pluginPermNotify', risky: false },
  { id: 'security_log', label: 'pluginPermSecurityLog', risky: false },
];

/** The specs for the ids a package holds, in display order; an id this build does not know is left out. */
export function permissionsOf(ids: readonly string[] | undefined): PermissionSpec[] {
  const held = new Set(ids ?? []);
  return PERMISSIONS.filter((p) => held.has(p.id));
}
