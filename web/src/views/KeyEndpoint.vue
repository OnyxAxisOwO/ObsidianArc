<script setup lang="ts">
import OaIconButton from '@/components/OaIconButton.vue';
import { t } from '@/composables/useI18n';
import { IconCopy } from '@/icons';

// The base URL is the other half of a key: it exists to be pasted into a client.
// The copy button is there because selecting monospace text out of a rounded box
// by hand is the part nobody enjoys.
defineProps<{ endpoint: string; copied: boolean }>();
defineEmits<{ (event: 'copy'): void }>();
</script>

<template>
  <div class="oa-key-endpoint">
    <div class="oa-key-endpoint-text">
      <span class="oa-field-label">{{ t('apiBaseUrl') }}</span>
      <code>{{ endpoint }}</code>
      <span class="oa-key-endpoint-hint">{{ t('apiProtocols') }}</span>
    </div>
    <OaIconButton
      class="oa-icon-btn"
      :label="copied ? t('copied') : t('copy')"
      @click="$emit('copy')"
    ><IconCopy :size="15" /></OaIconButton>
  </div>
</template>
