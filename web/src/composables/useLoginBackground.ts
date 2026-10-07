import { computed } from 'vue';
import { useMediaQuery } from '@vueuse/core';
import { useTheme } from '@/composables/useTheme';
import { siteInfo } from '@/stores/session';
import { pickBackground, type SiteBackground } from '@/theme/theme';

export function useLoginBackground() {
  const theme = useTheme();
  const isPortrait = useMediaQuery('(orientation: portrait)');

  const loginBg = computed<SiteBackground | null>(() => {
    const info = siteInfo.value;
    // An older server sends only the pictures, by URL.
    const set = info.backgrounds
      ?? Object.fromEntries(Object.entries(info.login_background ?? {}).map(([k, url]) => [k, { url, html: false }]));
    return pickBackground(set, '', isPortrait.value, theme.dark());
  });

  // Split for the templates: a picture is the root's own background, a
  // page is an OaBackdrop inside it.
  const loginBgUrl = computed(() => (loginBg.value && !loginBg.value.html ? loginBg.value.url : ''));
  const loginBgFrame = computed(() => (loginBg.value?.html ? loginBg.value.url : ''));

  return {
    loginBg,
    loginBgUrl,
    loginBgFrame,
    isPortrait,
  };
}
