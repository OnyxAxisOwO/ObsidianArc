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
import { t } from '@/composables/useI18n';
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
// Kept apart: a failed page has a retry that loads the page again, and a
// failed record has none, so one message could not say which it is.
const listError = ref('');
const openError = ref('');
// Each load and each open takes a ticket and only the newest answer is shown:
// a page change or a second row click does not wait for the one before it, so
// the older answer can land last.
let pageRequest = 0;
let openRequest = 0;

const columns = computed<Array<Column<Row>>>(() => props.spec.columns.map((column) => ({
  key: column.key,
  header: column.header(),
  ...(column.width ? { width: column.width } : {}),
  ...(column.secondary ? { secondary: true } : {}),
})));

async function load(): Promise<void> {
  const ticket = ++pageRequest;
  listError.value = '';
  try {
    const result = await props.spec.load(
      (paging.value.page - 1) * paging.value.pageSize, paging.value.pageSize);
    if (ticket !== pageRequest) return;
    rows.value = result.rows;
    total.value = result.total;
  } catch (failure) {
    if (ticket !== pageRequest) return;
    listError.value = failure instanceof ApiError ? failure.message : String(failure);
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
// Each record opened gets a key of its own. The panel keeps its drafts from
// setup, so two records sharing a key would show the first one's typing on the
// second; rows are not required to carry an id to tell them apart.
const openedKey = ref(0);

// One record open per page: another list taking the panel closes this one's.
// Only another list taking it counts: its closing leaves this list's own open
// in flight, which is still the record the reader last asked for.
const self = Symbol(props.spec.id);
const openOwner = useOpenRecord();
watch(openOwner, (owner) => {
  if (owner !== null && owner !== self) close();
});

// Closing also retires an open still in flight. Without that, a record the
// reader had already dismissed would appear once its detail arrived.
function close(): void {
  openRequest += 1;
  opened.value = null;
  openedRow.value = null;
  if (openOwner.value === self) openOwner.value = null;
}

async function open(row: Row): Promise<void> {
  if (!props.spec.detail) return;
  const ticket = ++openRequest;
  openError.value = '';
  try {
    const detail = await props.spec.detail(row);
    if (ticket !== openRequest) return;
    openOwner.value = self;
    opened.value = detail;
    openedRow.value = row;
    openedKey.value = ticket;
  } catch (failure) {
    if (ticket !== openRequest) return;
    openError.value = failure instanceof ApiError ? failure.message : String(failure);
  }
}

// An action may have moved the row (a pending report is now decided), so the
// table and the open record are both read again.
async function acted(): Promise<void> {
  // Read before the load: an open that starts while the table is read again
  // must be seen as newer than this refresh, or the record it fetched would
  // overwrite the one it opened.
  const before = openRequest;
  const row = openedRow.value;
  await load();
  if (!row || !props.spec.detail || openRequest !== before) return;
  try {
    const detail = await props.spec.detail(row);
    // Closing the panel during the fetch must not reopen it on this record.
    if (openRequest === before && openedRow.value === row) opened.value = detail;
  } catch {
    // The panel keeps what it showed; the action's own answer is on it.
  }
}

onMounted(load);
</script>

<template>
  <AdminControlCard :id="spec.id" class="oa-control-card-wide" :title="spec.title()" :hint="spec.hint?.() ?? ''" :icon="spec.icon ?? IconFile">
    <!-- The table stays under the error: its page controls are the only way to
         ask for another page, so hiding them leaves one failed page stuck. -->
    <p v-if="listError" class="oa-field-hint">
      {{ listError }}
      <button type="button" class="oa-btn small" @click="load()">{{ t('tryAgain') }}</button>
    </p>
    <p v-if="openError" class="oa-field-hint">{{ openError }}</p>
    <OaTable
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
    :key="openedKey"
    :detail="opened"
    @close="close"
    @done="acted"
  />
</template>
