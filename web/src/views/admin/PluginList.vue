<script setup lang="ts">
// A plugin's read-only table, paged the way the page's own tables are.
//
// What a cell says is the plugin's; that an account identifier in it is
// masked while safe mode is on is the backoffice's, so the plugin marks
// such cells and this component masks them.

import { computed, onMounted, ref, watch } from 'vue';
import { rememberedPageSize } from '@/lib/page-size';
import { ApiError } from '@/api/client';
import { maskUser } from '@/admin/safeMode';
import OaBadge from '@/components/OaBadge.vue';
import OaCellStack from '@/components/OaCellStack.vue';
import OaTable from '@/components/OaTable.vue';
import type { Column, PageState } from '@/components/table-types';
import { IconFile } from '@/icons';
import type { AdminListSpec, ListCell, RecordDetail } from '@/plugins/types';
import AdminControlCard from './AdminControlCard.vue';
import PluginRecordPanel from './PluginRecordPanel.vue';
import { useOpenRecord } from './openRecord';

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

// The record open beside the table, when the plugin lets its rows open.
const opened = ref<RecordDetail | null>(null);
const openedRow = ref<Row | null>(null);

// One record open per page: another list taking the panel closes this one's.
const self = Symbol(props.spec.id);
const openOwner = useOpenRecord();
watch(openOwner, (owner) => {
  if (owner !== self) close();
});

function close(): void {
  opened.value = null;
  openedRow.value = null;
  if (openOwner.value === self) openOwner.value = null;
}

async function open(row: Row): Promise<void> {
  if (!props.spec.detail) return;
  error.value = '';
  try {
    const detail = await props.spec.detail(row);
    openOwner.value = self;
    opened.value = detail;
    openedRow.value = row;
  } catch (failure) {
    error.value = failure instanceof ApiError ? failure.message : String(failure);
  }
}

// An action may have moved the row (a pending report is now decided), so the
// table and the open record are both read again.
async function acted(): Promise<void> {
  await load();
  if (openedRow.value && props.spec.detail) {
    try {
      opened.value = await props.spec.detail(openedRow.value);
    } catch {
      // The panel keeps what it showed; the action's own answer is on it.
    }
  }
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
      :selectable="!!spec.detail"
      @page="changePage"
      @select="open"
    >
      <template v-for="column in spec.columns" :key="column.key" #[`cell-${column.key}`]="{ row }">
        <OaBadge v-if="cell(column.key, row).badge" :tone="cell(column.key, row).badge!.tone">
          {{ cell(column.key, row).title }}
        </OaBadge>
        <OaCellStack v-else :title="cell(column.key, row).title" :sub="cell(column.key, row).sub" />
      </template>
    </OaTable>
  </AdminControlCard>

  <PluginRecordPanel
    v-if="opened"
    :key="String(openedRow?.id ?? '')"
    :detail="opened"
    @close="close"
    @done="acted"
  />
</template>
