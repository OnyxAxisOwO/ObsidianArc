<script setup lang="ts">
// The scheme cycle, light → dark → auto.
//
// It lives in the header, on the sign-in card and on the landing page: this
// may be the first thing anybody sees, and being stuck in the wrong scheme
// until you have an account is an odd first impression.

import { computed } from 'vue';
import { t } from '@/composables/useI18n';
import { useTheme } from '@/composables/useTheme';
import { persistTheme } from '@/stores/session';
import { IconAuto, IconMoon, IconSun } from '@/icons';
import OaIconButton from './OaIconButton.vue';

const theme = useTheme();
const glyph = computed(() => {
  const mode = theme.mode();
  return mode === 'dark' ? IconMoon : mode === 'light' ? IconSun : IconAuto;
});
</script>

<template>
  <OaIconButton class="oa-icon-btn" :label="t('theme')" @click="persistTheme(theme.next())">
    <component :is="glyph" :size="17" />
  </OaIconButton>
</template>
