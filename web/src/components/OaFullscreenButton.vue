<script setup lang="ts">
// A panel's full-screen switch, in the actions slot of every panel that can
// take the whole window. The state stays with each panel's usePanelFullscreen,
// because the panel is what grows; this is only the button that asks it to.

import { t } from '@/composables/useI18n';
import { IconCollapse, IconExpand } from '@/icons';
import OaIconButton from './OaIconButton.vue';

defineProps<{ fullscreen: boolean }>();
defineEmits<{ toggle: [] }>();
</script>

<template>
  <OaIconButton
    class="oa-icon-btn"
    :label="t(fullscreen ? 'exitFullscreen' : 'fullscreen')"
    @click="$emit('toggle')"
  >
    <IconCollapse v-if="fullscreen" :size="16" />
    <IconExpand v-else :size="16" />
  </OaIconButton>
</template>
