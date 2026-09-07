<script setup lang="ts">
// How one request ended, in a colour.

import OaBadge from '@/components/OaBadge.vue';
import OaBadgeRow from '@/components/OaBadgeRow.vue';
import { t } from '@/composables/useI18n';

const props = defineProps<{
  status: string;
  errorCode?: string | undefined;
}>();
</script>

<template>
  <OaBadge v-if="props.status === 'ok'" tone="muted">{{ t('statusOk') }}</OaBadge>
  <OaBadge v-else-if="props.status === 'aborted'" tone="muted">{{ t('statusStopped') }}</OaBadge>
  <OaBadgeRow v-else>
    <OaBadge tone="danger">
      {{ props.status === 'rejected' ? t('statusRefused') : t('statusFailed') }}
    </OaBadge>
    <OaBadge v-if="props.errorCode" tone="muted">{{ props.errorCode }}</OaBadge>
  </OaBadgeRow>
</template>
