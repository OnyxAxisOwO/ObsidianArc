<script setup lang="ts">
import { computed } from 'vue';
import { t } from '@/composables/useI18n';
import { PAGE_SIZES, rememberPageSize } from '@/lib/page-size';
import OaSelect from './OaSelect.vue';
import type { PageState } from './table-types';

const props = defineProps<PageState & { total: number; busy?: boolean | undefined }>();
const emit = defineEmits<{ (event: 'change', next: PageState): void }>();
const pages = computed(() => Math.max(1, Math.ceil(props.total / props.pageSize)));
const current = computed(() => Math.min(pages.value, Math.max(1, props.page)));
function go(page: number, pageSize = props.pageSize): void {
  if (props.busy) return;
  if (pageSize !== props.pageSize) rememberPageSize(pageSize);
  emit('change', { page, pageSize });
}
</script>

<!-- Nothing to page through draws nothing, and one page draws only the count:
     "1 / 1" between two disabled arrows is four controls that do nothing,
     under every short table in the backoffice. The size menu stays with the
     arrows, because it only matters once there is a second page to avoid. -->
<template>
  <nav v-if="total > 0 && pages === 1" class="oa-pagination compact" :aria-label="t('pagination')">
    <span class="oa-pagination-count" aria-live="polite">{{ t('pageSummary', { first: 1, last: total, total }) }}</span>
  </nav>
  <nav v-else-if="total > 0" class="oa-pagination" :aria-label="t('pagination')">
    <div class="oa-pagination-size">
      <span>{{ t('rowsPerPage') }}</span>
      <OaSelect
        class="oa-filter-select"
        :aria-label="t('rowsPerPage')"
        :model-value="String(pageSize)"
        :disabled="busy"
        :searchable="false"
        :choices="PAGE_SIZES.map((size) => ({ value: String(size), label: String(size) }))"
        @update:model-value="go(1, Number($event))"
      />
    </div>
    <span class="oa-pagination-count" aria-live="polite">{{ t('pageSummary', {
      first: total ? (current - 1) * pageSize + 1 : 0,
      last: Math.min(current * pageSize, total), total,
    }) }}</span>
    <div class="oa-pagination-actions">
      <button type="button" class="oa-btn" :disabled="busy || current <= 1" :aria-label="t('firstPage')" @click="go(1)">«</button>
      <button type="button" class="oa-btn" :disabled="busy || current <= 1" @click="go(current - 1)">{{ t('previous') }}</button>
      <span>{{ t('pageNumber', { page: current, pages }) }}</span>
      <button type="button" class="oa-btn" :disabled="busy || current >= pages" @click="go(current + 1)">{{ t('next') }}</button>
      <button type="button" class="oa-btn" :disabled="busy || current >= pages" :aria-label="t('lastPage')" @click="go(pages)">»</button>
    </div>
  </nav>
</template>
