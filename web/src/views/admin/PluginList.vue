<script setup lang="ts">
// A plugin's read-only table, paged the way the page's own tables are.
//
// What a cell says is the plugin's; that an account identifier in it is
// masked while safe mode is on is the backoffice's, so the plugin marks
// such cells and this component masks them.

import { computed, onMounted, ref } from 'vue';
import { rememberedPageSize } from '@/lib/page-size';
import { ApiError } from '@/api/client';
import { maskUser } from '@/admin/safeMode';
import OaBadge from '@/components/OaBadge.vue';
import OaCellStack from '@/components/OaCellStack.vue';
import OaTable from '@/components/OaTable.vue';
import type { Column, PageState } from '@/components/table-types';
import { IconFile } from '@/icons';
import type { AdminListSpec, ListCell } from '@/plugins/types';
import AdminControlCard from './AdminControlCard.vue';

type Row = Record<string, unknown>;

const props = defineProps<{ spec: AdminListSpec }>();

const rows = ref<Row[]>([]);
const total = ref(0);
const paging = ref<PageState>({ page: 1, pageSize: rememberedPageSize() });
const error = ref('');

const columns = computed<Array<Column<Row>>>(() => props.spec.columns.map((column) => ({
  key: column.key,
  header: column.header(),
  ...(column.width ? { width: column.width } : {}),
  ...(column.secondary ? { secondary: true } : {}),
})));

async function load(): Promise<void> {
  error.value = '';
  try {
    const result = await props.spec.load(
      (paging.value.page - 1) * paging.value.pageSize, paging.value.pageSize);
    rows.value = result.rows;
    total.value = result.total;
  } catch (failure) {
    error.value = failure instanceof ApiError ? failure.message : String(failure);
  }
}

function changePage(next: PageState): void {
  paging.value = next;
  void load();
}

function cell(key: string, row: Row): ListCell {
  const value = props.spec.cell(key, row);
  if (!value.mask) return value;
  return {
    ...value,
    title: maskUser(value.title),
    ...(value.sub ? { sub: maskUser(value.sub) } : {}),
  };
}

onMounted(load);
</script>

<template>
  <AdminControlCard :id="spec.id" class="oa-control-card-wide" :title="spec.title()" :hint="spec.hint?.() ?? ''" :icon="spec.icon ?? IconFile">
    <p v-if="error" class="oa-field-hint">{{ error }}</p>
    <OaTable
      v-else
      :pagination="{ ...paging, total }"
      :columns="columns"
      :rows="rows"
      :empty="spec.empty()"
      @page="changePage"
    >
      <template v-for="column in spec.columns" :key="column.key" #[`cell-${column.key}`]="{ row }">
        <OaBadge v-if="cell(column.key, row).badge" :tone="cell(column.key, row).badge!.tone">
          {{ cell(column.key, row).title }}
        </OaBadge>
        <OaCellStack v-else :title="cell(column.key, row).title" :sub="cell(column.key, row).sub" />
      </template>
    </OaTable>
  </AdminControlCard>
</template>
