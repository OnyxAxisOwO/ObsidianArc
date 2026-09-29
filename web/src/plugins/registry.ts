// Which plugins this instance runs, and what they said.
//
// The server names its compiled-in plugins in /api/site ("plugins"), and only
// those are fetched: every plugin's browser half is built into the bundle,
// each as a chunk of its own, and an
// instance whose binary has none of them never downloads a byte of any.
//
// The list is fixed once the session starts, because the server's is fixed
// once the process does.

import { shallowRef } from 'vue';
import { currentLanguage } from '@/composables/useI18n';
import type { AccountFieldSpec, ArcPlugin, GuardSpec, NotificationText, PluginConfig, Text } from './types';


// Each plugin's entry is <name>/<name>.plugin.ts rather than index.ts, so the
// chunk the bundler makes for it is named for the plugin — and so a plugin's
// chunk holds only what nothing on the first paint already has: the bundler
// knows the entry is loaded by the time this import runs.
const modules = import.meta.glob<{ default: ArcPlugin }>('./*/*.plugin.ts');

const loaded = shallowRef<readonly ArcPlugin[]>([]);
let configs: Record<string, PluginConfig> = {};

/**
 * Fetches the named plugins' code. A name this build has no code for is
 * skipped: a server newer than its frontend is not a reason to stop booting,
 * and the core works without any plugin's half.
 */
export async function loadPlugins(blocks: Record<string, PluginConfig> | undefined): Promise<void> {
  configs = blocks ?? {};
  const names = Object.keys(configs).sort();
  const found = await Promise.all(names.map(async (name) => {
    const load = modules[`./${name}/${name}.plugin.ts`];
    if (!load) return null;
    try {
      return (await load()).default;
    } catch (failure) {
      console.warn(`plugin ${name} could not be loaded`, failure);
      return null;
    }
  }));
  loaded.value = found.filter((plugin): plugin is ArcPlugin => plugin !== null);
}

/** Every loaded plugin, in name order. Reactive: reading it subscribes. */
export function plugins(): readonly ArcPlugin[] {
  return loaded.value;
}

/** What the server said for one plugin, or an empty block. */
export function pluginConfig(name: string): PluginConfig {
  return configs[name] ?? {};
}

/** Tests stand an instance up with a plugin already in place. */
export function installPlugins(list: ArcPlugin[], blocks: Record<string, PluginConfig> = {}): void {
  configs = blocks;
  loaded.value = list;
}

/** The spec for an account field, when a loaded plugin owns it. */
export function fieldSpec(key: string): AccountFieldSpec | undefined {
  for (const plugin of loaded.value) {
    const spec = plugin.fields?.[key];
    if (spec) return spec;
  }
  return undefined;
}

export function guards(): Array<{ guard: GuardSpec; config: PluginConfig }> {
  return loaded.value.flatMap((plugin) =>
    (plugin.guards ?? []).map((guard) => ({ guard, config: pluginConfig(plugin.name) })));
}

function lookup(pick: (plugin: ArcPlugin) => Record<string, Text> | undefined, key: string): string | null {
  for (const plugin of loaded.value) {
    const text = pick(plugin)?.[key];
    if (text) return text();
  }
  return null;
}

/** A plugin's wording for an API error code, or null. Field codes are included. */
export function pluginRefusal(code: string): string | null {
  const own = lookup((plugin) => plugin.refusals, code);
  if (own) return own;
  const match = /^(?:invalid_(.+)|(.+)_(taken|required))$/.exec(code);
  if (!match) return null;
  const spec = fieldSpec(match[1] ?? match[2] ?? '');
  if (!spec) return null;
  if (match[1]) return spec.invalid();
  return match[3] === 'taken' ? spec.taken() : spec.required();
}

export function pluginOAuthError(code: string): string | null {
  const own = lookup((plugin) => plugin.oauthErrors, code);
  if (own) return own;
  const required = /^(.+)_required$/.exec(code);
  return required ? fieldSpec(required[1]!)?.required() ?? null : null;
}

/** Why provider's connection cannot be removed, when a plugin pins it. */
export function pinnedProvider(provider: string): { hint: Text; refused: Text } | null {
  for (const plugin of loaded.value) {
    const pinned = plugin.pinnedProviders?.[provider];
    if (pinned) return pinned;
  }
  return null;
}

export function pluginNotification(kind: string, params: Record<string, unknown>): NotificationText | null {
  for (const plugin of loaded.value) {
    const word = plugin.notifications?.[kind];
    if (word) return word(params);
  }
  return null;
}

/** A plugin's line under an invitee in the inviter's own list, or null. */
export function pluginInviteeNote(invitee: Record<string, unknown>): string | null {
  for (const plugin of loaded.value) {
    const note = plugin.inviteeNote?.(invitee);
    if (note) return note;
  }
  return null;
}

export function pluginSecurityEvent(event: string): string | null {
  return lookup((plugin) => plugin.securityEvents, event);
}

export function pluginSecurityReason(reason: string): string | null {
  return lookup((plugin) => plugin.securityReasons, reason);
}

// A plugin's own dictionary. Here rather than in a module of its own so the
// helper every plugin uses lives in the entry, beside the registry that
// fetches them, and not in a chunk each plugin would have to fetch first.
//
//
// The core's is i18n.ts, and a plugin's sentences do not belong in it: a
// build without the plugin would carry them in the main bundle, and the
// Chinese chunk would carry them twice. So each plugin keeps both languages
// beside its code, typed the way i18n.ts is — English is the source of truth
// and the Chinese map must have every key — and resolves through
// currentLanguage(), which reads the same version counter t() does, so a
// language switch redraws a plugin's text with everything else.

export function pluginStrings<const E extends Record<string, string>>(
  en: E,
  zh: Record<keyof E, string>,
): (key: keyof E, vars?: Record<string, string | number>) => string {
  return (key, vars) => {
    const template = (currentLanguage() === 'zh' ? zh[key] : en[key]) ?? en[key];
    if (!vars) return template;
    return template.replace(/\{(\w+)\}/g, (whole, name: string) =>
      name in vars ? String(vars[name]) : whole);
  };
}
