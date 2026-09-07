// The dictionary, made reactive.
//
// `i18n.ts` is unchanged from the standalone build and stays the source of
// truth: one `en` object, `StringKey` derived from it, and a `zh` map the
// compiler forces to be complete. What it does not have is any notion of a
// render — it was written for code that redrew a screen by hand.
//
// This is the whole adaptation: a version counter that every translated
// string reads. Changing the language bumps it, which invalidates every
// component that called `t()` during its render, and Vue redraws exactly
// those. No dictionary is copied into a store and no key is duplicated.

import { ref } from 'vue';
import {
  language,
  loadLanguage,
  setLanguage as applyLanguage,
  t as translate,
  tn as translatePlural,
  type Language,
  type StringKey,
} from '@/i18n';

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

/** Called once at boot, before the first render. */
export async function primeLanguage(): Promise<void> {
  await loadLanguage();
  version.value += 1;
}
