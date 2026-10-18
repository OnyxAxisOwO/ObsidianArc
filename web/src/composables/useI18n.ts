// The dictionary, made reactive.
//
// `i18n.ts` is the source of truth: one `en` object (with the backoffice's
// half in i18n.admin.ts), `StringKey` derived from them, and a `zh` map the
// compiler forces to be complete. It has no notion of a
// render, which is why the reactive layer lives here rather than in it.
//
// This is the whole adaptation: a version counter that every translated
// string reads. Changing the language bumps it, which invalidates every
// component that called `t()` during its render, and Vue redraws exactly
// those. No dictionary is copied into a store and no key is duplicated.

import { ref } from 'vue';
import {
  fallBackToEnglish,
  language,
  loadLanguage,
  setLanguage as applyLanguage,
  t as translate,
  tn as translatePlural,
  type Language,
  type StringKey,
} from '@/i18n';
import { within } from '@/lib/deadline';

export type { Language, StringKey };

const version = ref(0);

/** Looks up a string, substituting {name} placeholders. */
export function t(key: StringKey, vars?: Record<string, string | number>): string {
  // Read, never written here: this is what subscribes the calling render to
  // a later language change.
  void version.value;
  return translate(key, vars);
}

/** Picks between two keys by count. See `tn` in i18n.ts for why. */
export function tn(
  count: number,
  one: StringKey,
  other: StringKey,
  vars?: Record<string, string | number>,
): string {
  void version.value;
  return translatePlural(count, one, other, vars);
}

export function currentLanguage(): Language {
  void version.value;
  return language();
}

/**
 * Switches language and repaints.
 *
 * The bump happens after the chunk has landed, so nothing renders in the
 * half-state where the language has changed and its dictionary has not
 * arrived — which would flash English at a reader who just asked for Chinese.
 */
export async function changeLanguage(next: Language): Promise<void> {
  await applyLanguage(next);
  version.value += 1;
}

// Same limit as a plugin package's browser half: boot waits on both before the
// first paint, and neither may hold the spinner for longer than this.
const DICTIONARY_DEADLINE_MS = 10_000;

/**
 * Called once at boot, before the first render.
 *
 * A dictionary that does not arrive must not hold up the mount: the boot
 * waits on this alongside the session, and a rejection here would leave the
 * page on its spinner. The page falls back to English through language() as
 * well as t(), because a component that keys a date locale off language()
 * would otherwise format English text with Chinese dates. The choice stays
 * stored, so the next boot asks for the dictionary again.
 *
 * A dictionary that is accepted and never answers is treated as one that
 * failed, for the same reason.
 */
export async function primeLanguage(): Promise<void> {
  try {
    await within(loadLanguage(), DICTIONARY_DEADLINE_MS);
  } catch (failure) {
    fallBackToEnglish();
    console.warn('language dictionary did not load; the page stays in English', failure);
  }
  version.value += 1;
}
