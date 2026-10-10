// The side panels that may stand beside the backoffice, and how each is named.
//
// A panel drawn over the backoffice is named by the `panel` parameter of the
// backoffice's own URL (composables/usePanelExit.ts): `settings`,
// `settings/security`, `x/<slug>/<entry>`. The first segment picks the
// component, and what follows it becomes the props the chat's route gives the
// same panel. The chat's routes in router/index.ts keep their own addresses and
// give the same props from them, so a panel is drawn the same way in both.

import type { Component } from 'vue';
import { canAdmin, siteInfo } from '@/stores/session';
import AboutPanel from './AboutPanel.vue';
import ArchivePanel from './ArchivePanel.vue';
import FeedbackPanel from './FeedbackPanel.vue';
import ImageLabPanel from './ImageLabPanel.vue';
import KeysPanel from './KeysPanel.vue';
import LeaderboardPanel from './LeaderboardPanel.vue';
import PluginUserPanel from './PluginUserPanel.vue';
import SettingsPanel from './SettingsPanel.vue';
import UptimePanel from './UptimePanel.vue';
import UsagePanel from './UsagePanel.vue';

interface PanelEntry {
  component: Component;
  /** Props from the segments after the name; null when the address is incomplete. */
  props?: (rest: string[]) => Record<string, string | undefined> | null;
}

const PANELS: Record<string, PanelEntry> = {
  settings: { component: SettingsPanel, props: ([tab]) => ({ tab }) },
  usage: { component: UsagePanel },
  archive: { component: ArchivePanel },
  keys: { component: KeysPanel },
  feedback: { component: FeedbackPanel },
  about: { component: AboutPanel },
  'image-lab': { component: ImageLabPanel },
  uptime: { component: UptimePanel },
  leaderboard: { component: LeaderboardPanel },
  x: { component: PluginUserPanel, props: ([slug, entry]) => (slug ? { slug, entry } : null) },
};

export interface SidePanel {
  /**
   * The first segment. It keys the drawn panel, so moving within one of them
   * (another tab, another entry) keeps the component, as the chat does.
   */
  name: string;
  component: Component;
  props: Record<string, string | undefined>;
}

/** Resolves the `panel` parameter, or null when it names nothing that can be drawn. */
export function resolveSidePanel(value: unknown): SidePanel | null {
  if (typeof value !== 'string' || !value) return null;
  const [name = '', ...rest] = value.split('/');
  // An own-property check, so a name such as `constructor` is not a panel.
  if (!Object.hasOwn(PANELS, name)) return null;
  const entry = PANELS[name];
  if (!entry) return null;
  const props = entry.props ? entry.props(rest) : {};
  return props ? { name, component: entry.component, props } : null;
}

/**
 * Whether the reader may open a panel at all.
 *
 * Uptime and the leaderboard are for everybody once the operator publishes
 * them, and for the people who look after them before that. The chat's own
 * address for a panel and the backoffice's parameter for it both answer to
 * this one rule, which is why it lives here rather than in either.
 */
export function panelAllowed(name: string): boolean {
  if (name === 'uptime') return canAdmin('availability') || !!siteInfo.value.health_show_users;
  if (name === 'leaderboard') return canAdmin('leaderboard') || !!siteInfo.value.leaderboard_show_users;
  return true;
}
