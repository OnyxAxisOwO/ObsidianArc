// Which plugins this instance runs, and what they said.
//
// The server names its enabled plugins in /api/site ("plugins"), and only
// those are fetched: every plugin's browser half is built into the bundle,
// each as a chunk of its own, and an instance that runs none of them never
// downloads a byte of any.
//
// The list changes when an administrator switches a plugin on or off; the
// plugins screen asks the session to read /api/site again, and this module
// loads whatever it now names. Everybody else meets the change on their next
// page load, which is when the server's own answer changed for them too.

import { shallowRef } from 'vue';
import { api, ApiError } from '@/api/client';
import { currentLanguage } from '@/composables/useI18n';
import * as icons from '@/icons';
import { absoluteTime } from '@/lib/format';
import type {
  AccountFieldSpec, ArcPlugin, GuardSpec, NotificationText, PluginConfig, PluginFactory, PluginHost, Text,
} from './types';


// Each plugin's entry is <name>/<name>.plugin.ts rather than index.ts, so the
// chunk the bundler makes for it is named for the plugin — and so a plugin's
// chunk holds only what nothing on the first paint already has: the bundler
// knows the entry is loaded by the time this import runs.
const modules = import.meta.glob<{ default: ArcPlugin }>('./*/*.plugin.ts');

const loaded = shallowRef<readonly ArcPlugin[]>([]);
let configs: Record<string, PluginConfig> = {};

/**
 * How a package's module is fetched: a dynamic import of the address the
 * server gave. A test replaces it, since the address is served by a server
 * the test does not have.
 */
type ModuleImporter = (url: string) => Promise<{ default?: unknown }>;

let importModule: ModuleImporter = (url) => import(/* @vite-ignore */ url);

export function setModuleImporter(importer: ModuleImporter | null): void {
  importModule = importer ?? ((url) => import(/* @vite-ignore */ url));
}

/** What a package's module is handed; see PluginHost. */
export function pluginHost(): PluginHost {
  return {
    api, ApiError, icons, strings: pluginStrings, format: { absoluteTime },
    language: () => (currentLanguage() === 'zh' ? 'zh' : 'en'),
  };
}

/**
 * Fetches the named plugins' code: a package's from the address its block
 * carries (`_ui`), a compiled-in one's from this build. A name with neither is
 * skipped: a server newer than its frontend is not a reason to stop booting,
 * and the core works without any plugin's half.
 */
export async function loadPlugins(blocks: Record<string, PluginConfig> | undefined): Promise<void> {
  configs = blocks ?? {};
  const names = Object.keys(configs).sort();
  const found = await Promise.all(names.map((name) => {
    const ui = configs[name]?.['_ui'];
    return typeof ui === 'string' && ui ? loadPackageModule(name, ui) : loadPluginModule(name);
  }));
  loaded.value = found.filter((plugin): plugin is ArcPlugin => plugin !== null);
}

/**
 * A package's browser half: its module fetched from the server, called with
 * what the page lends it. A module that fails to load, is not a function, or
 * answers with a declaration for another plugin is left out with a warning —
 * the plugin's server half still works; only its screens are missing.
 */
export async function loadPackageModule(name: string, url: string): Promise<ArcPlugin | null> {
  try {
    const module = await importModule(url);
    if (typeof module.default !== 'function') {
      console.warn(`plugin ${name}: its browser half does not export a function`);
      return null;
    }
    const plugin = await (module.default as PluginFactory)(pluginHost());
    if (!plugin || plugin.name !== name) {
      console.warn(`plugin ${name}: its browser half declares ${plugin?.name ?? 'nothing'}`);
      return null;
    }
    return plugin;
  } catch (failure) {
    console.warn(`plugin ${name} could not be loaded`, failure);
    return null;
  }
}

/**
 * One plugin's browser half whether or not the server runs it, for the
 * plugins screen: its install dialog draws the settings the plugin declares
 * before there is anything running to declare them. Null when this build has
 * no browser half for it.
 */
export async function loadPluginModule(name: string): Promise<ArcPlugin | null> {
  const load = modules[`./${name}/${name}.plugin.ts`];
  if (!load) return null;
  try {
    return (await load()).default;
  } catch (failure) {
    console.warn(`plugin ${name} could not be loaded`, failure);
    return null;
  }
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
