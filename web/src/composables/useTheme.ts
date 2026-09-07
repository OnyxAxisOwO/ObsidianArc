// Theme and accent, made reactive.
//
// `theme/theme.ts` is unchanged: it still owns localStorage, the pre-paint
// palette cache and the maths that turns one hue into a whole interface. It
// already published changes through `onThemeChange`, which existed so the
// hand-written screens could repaint themselves.
//
// That subscription is now a single `ref` the header, the settings panel and
// the charts all read, so a theme change repaints whatever is on screen
// without any of them knowing about the others.

import { onScopeDispose, readonly, ref, type Ref } from 'vue';
import {
  accentPreference,
  isDark,
  nextThemeMode,
  onThemeChange,
  themeMode,
  wallpaper,
  type ThemeMode,
  type Wallpaper,
} from '@/theme/theme';
import type { AccentPreference } from '@/theme/color-utils';

export type { ThemeMode, Wallpaper };

const version = ref(0);
let subscribed = false;

function subscribe(): void {
  if (subscribed) return;
  subscribed = true;
  // Never unsubscribed: this is the application's own listener, alive for as
  // long as the document is, and dropping it would leave the interface
  // painted in a scheme nobody chose.
  onThemeChange(() => {
    version.value += 1;
  });
}

export function useTheme(): {
  version: Readonly<Ref<number>>;
  mode(): ThemeMode;
  dark(): boolean;
  accent(): AccentPreference;
  paper(): Wallpaper | null;
  next(): ThemeMode;
} {
  subscribe();
  return {
    version: readonly(version),
    mode: () => (void version.value, themeMode()),
    dark: () => (void version.value, isDark()),
    accent: () => (void version.value, accentPreference()),
    paper: () => (void version.value, wallpaper()),
    next: () => nextThemeMode(),
  };
}

/**
 * Runs a callback whenever the palette changes.
 *
 * For the two things that cannot simply re-render from a token — the charts,
 * which read `--ai-primary` off the root and derive a ramp from it in
 * JavaScript. Everything else should use a `--ai-*` property and need none
 * of this.
 */
export function onPaletteChange(run: () => void): void {
  const stop = onThemeChange(run);
  onScopeDispose(() => stop());
}
