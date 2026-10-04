<script setup lang="ts">
// The strip that appears under a table once rows are ticked.
//
// It sticks to the bottom of the scrolling body rather than sitting above the
// table: the reader ticks rows from wherever they have scrolled to, and a bar
// at the top would be off the screen by then. The actions are the page's, in
// the default slot; delete is here because it is the same two-click button
// everywhere and not a thing each page should reinvent.

import { t } from '@/composables/useI18n';
import OaConfirmButton from './OaConfirmButton.vue';

const props = defineProps<{
  count: number;
  /** How many rows could be ticked, for "select all". */
  total: number;
  busy?: boolean;
  error?: string;
  deletable?: boolean;
  /** Replaces the delete question, for a delete that takes more than the rows. */
  deleteQuestion?: string;
}>();

const emit = defineEmits<{
  (event: 'all'): void;
  (event: 'clear'): void;
  (event: 'delete'): void;
}>();
</script>

<template>
  <div v-if="props.count" class="oa-bulk-bar" role="toolbar" :aria-busy="props.busy">
    <div class="oa-bulk-row">
      <span class="oa-bulk-count">{{ t('bulkSelected', { count: props.count }) }}</span>
      <button
        v-if="props.count < props.total"
        type="button"
        class="oa-btn small"
        :disabled="props.busy"
        @click="emit('all')"
      >{{ t('bulkSelectAll', { count: props.total }) }}</button>
      <slot />
      <OaConfirmButton
        v-if="props.deletable"
        class="oa-btn small oa-btn-danger"
        :label="t('bulkDelete')"
        :armed-title="props.deleteQuestion ?? t('bulkDeleteConfirm', { count: props.count })"
        :disabled="props.busy"
        @confirm="emit('delete')"
      />
      <button
        type="button"
        class="oa-btn small oa-bulk-clear"
        :disabled="props.busy"
        @click="emit('clear')"
      >{{ t('bulkClear') }}</button>
    </div>
    <p v-if="props.error" class="oa-bulk-error" role="alert">{{ props.error }}</p>
  </div>
</template>
