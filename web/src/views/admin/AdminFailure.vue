<script setup lang="ts">
// A load that did not come back, in place, with a way to try again.

import { t } from '@/composables/useI18n';

const props = defineProps<{ message: string }>();
const emit = defineEmits<{ (event: 'retry'): void }>();
</script>

<template>
  <div class="oa-notice">
    <h2 class="oa-notice-title">{{ t('couldNotLoad') }}</h2>
    <p class="oa-notice-body">{{ props.message }}</p>
    <button type="button" class="oa-btn" @click="emit('retry')">{{ t('tryAgain') }}</button>
  </div>
</template>
