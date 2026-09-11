<script setup lang="ts">
// What this instance is costing the machine it runs on.
//
// Three questions an operator asks when something feels wrong and the logs
// say nothing: whose files are filling the disk, how much memory the process
// is holding, and whether it is actually busy. The first is the one with a
// name attached, so it gets the table.
//
// The page re-reads itself while it is open, because a CPU figure that does
// not move is not a CPU figure.

import { computed, onMounted, ref } from 'vue';
import { useIntervalFn } from '@vueuse/core';
import { adminApi, type Resources, type UserStorage } from '@/admin/api';
import OaAdminSection from '@/components/OaAdminSection.vue';
import OaStatGrid from '@/components/OaStatGrid.vue';
import OaTable from '@/components/OaTable.vue';
import type { Column, SortState } from '@/components/table-types';
import type { Stat } from '@/components/stat';
import { t } from '@/composables/useI18n';
import { formatBytes } from '@/lib/format';
import AdminFailure from './AdminFailure.vue';
import { useAdminView } from './adminView';

const REFRESH_MS = 5000;

const view = useAdminView();
view.setTitle(t('navResources'), t('resourcesSubtitle'));

const snapshot = ref<Resources | null>(null);
const error = ref('');
const sort = ref<SortState | null>(null);

const storageStats = computed<Stat[]>(() => {
  const storage = snapshot.value?.storage;
  if (!storage) return [];
  return [
    { label: t('resHeld'), value: formatBytes(storage.held_bytes), note: t('resHeldNote') },
    { label: t('resFiles'), value: String(storage.held_count) },
    { label: t('resDiscarded'), value: String(storage.discarded_count), note: t('resDiscardedNote') },
  ];
});

const memoryStats = computed<Stat[]>(() => {
  const memory = snapshot.value?.memory;
  if (!memory) return [];
  return [
    // Heap in use answers "is this leaking"; what the process took from the
    // operating system is what a container's limit is measured against.
    { label: t('resHeap'), value: formatBytes(memory.heap_bytes), note: t('resHeapNote') },
    { label: t('resProcessMemory'), value: formatBytes(memory.sys_bytes), note: t('resProcessMemoryNote') },
    { label: t('resGoroutines'), value: String(memory.goroutines) },
    {
      label: t('resGC'),
      value: String(memory.gc_count),
      note: t('resGCPause', { ms: memory.gc_pause_ms.toFixed(2) }),
    },
  ];
});

const cpuStats = computed<Stat[]>(() => {
  const cpu = snapshot.value?.cpu;
  if (!cpu) return [];
  return [
    {
      label: t('resCPUShare'),
      // Of one core, which is why the count is right beside it: 240% on an
      // eight-core box is busy, and on a two-core box it is saturated.
      value: cpu.percent === undefined ? t('resMeasuring') : `${cpu.percent.toFixed(1)}%`,
      note: cpu.window_sec ? t('resCPUWindow', { sec: Math.round(cpu.window_sec) }) : t('resCPUFirst'),
    },
    { label: t('resCores'), value: String(cpu.cores), note: t('resGomaxprocs', { n: cpu.gomaxprocs }) },
    {
      label: t('resCPUTime'),
      value: cpu.process_sec === undefined ? '—' : duration(cpu.process_sec),
      note: t('resCPUTimeNote'),
    },
  ];
});

const columns = computed<Array<Column<UserStorage>>>(() => [
  { key: 'account', header: t('colAccount'), text: (row) => row.name },
  { key: 'files', header: t('resFiles'), text: (row) => String(row.count), numeric: true, width: '90px' },
  {
    key: 'held',
    header: t('resHeld'),
    text: (row) => formatBytes(row.bytes),
    numeric: true,
    width: '110px',
    sort: (row) => row.bytes,
  },
]);

/** Seconds of CPU as hours, minutes and seconds — it is a total, not a clock. */
function duration(seconds: number): string {
  if (seconds < 60) return `${seconds.toFixed(1)}s`;
  const minutes = Math.floor(seconds / 60);
  if (minutes < 60) return `${minutes}m ${Math.round(seconds % 60)}s`;
  return `${Math.floor(minutes / 60)}h ${minutes % 60}m`;
}

async function load(): Promise<void> {
  error.value = '';
  try {
    snapshot.value = await adminApi.resources();
  } catch (failure) {
    error.value = failure instanceof Error ? failure.message : String(failure);
  }
}

// It reads the page again rather than re-rendering it through the router, so
// a table somebody is looking at does not jump. A failed refresh leaves what
// is on screen: an operator watching a struggling instance would rather see
// the last good numbers than an error where they were.
useIntervalFn(() => {
  void adminApi.resources().then((next) => { snapshot.value = next; }).catch(() => {});
}, REFRESH_MS);

onMounted(load);
</script>

<template>
  <Teleport :to="view.actionsHost">
    <button type="button" class="oa-btn" @click="view.reload()">{{ t('refresh') }}</button>
  </Teleport>

  <AdminFailure v-if="error" :message="error" @retry="load" />
  <p v-else-if="!snapshot" class="oa-table-empty">{{ t('loading') }}</p>

  <template v-else>
    <OaAdminSection id="resStorage" :title="t('resStorage')">
      <OaStatGrid :stats="storageStats" />
    </OaAdminSection>

    <OaTable
      :columns="columns"
      :rows="snapshot.storage.by_user"
      :empty="t('resNoFiles')"
      :sort="sort"
      @sort="sort = $event"
    />

    <OaAdminSection id="resMemory" :title="t('resMemory')">
      <OaStatGrid :stats="memoryStats" />
    </OaAdminSection>

    <OaAdminSection id="resCPU" :title="t('resCPU')">
      <OaStatGrid :stats="cpuStats" />
    </OaAdminSection>

    <p class="oa-field-hint">{{ t('resNoDatabaseSize') }}</p>
  </template>
</template>
