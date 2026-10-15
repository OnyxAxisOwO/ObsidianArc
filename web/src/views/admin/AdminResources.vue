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

import { computed, onMounted, ref, watch } from 'vue';
import { useDocumentVisibility, useIntervalFn } from '@vueuse/core';
import { adminApi, type Resources, type UserStorage } from '@/admin/api';
import AdminControlCard from './AdminControlCard.vue';
import { IconFile, IconCpu, IconLayers } from '@/icons';
import OaTable from '@/components/OaTable.vue';
import type { Column, SortState } from '@/components/table-types';
import type { Stat } from '@/components/stat';
import { t } from '@/composables/useI18n';
import { isMasked, maskUser } from '@/admin/safeMode';
import { formatBytes } from '@/lib/format';
import AdminFailure from './AdminFailure.vue';
import { useAdminView } from './adminView';

const REFRESH_MS = 5000;

const view = useAdminView();
view.setTitle(() => t('navResources'), () => t('controlResourceHint'));

const snapshot = ref<Resources | null>(null);
const error = ref('');
const refreshing = ref(false);
const updatedAt = computed(() => snapshot.value ? new Date(snapshot.value.sampled_at).toLocaleTimeString([], { hour: '2-digit', minute: '2-digit', second: '2-digit' }) : '');
const sort = ref<SortState | null>(null);

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
  { key: 'account', header: t('colAccount'), text: (row) => maskUser(row.name) },
  { key: 'files', header: t('resFiles'), text: (row) => String(row.count), numeric: true, width: '90px' },
  {
    key: 'held',
    header: t('resHeld'),
    text: (row) => formatBytes(row.bytes),
    numeric: true,
    width: '150px',
    sort: (row) => row.bytes,
  },
]);

/** Seconds of CPU as hours, minutes and seconds — it is a total, not a clock. */
function duration(seconds: number): string {
  // Judged at the precision it is printed to: 59.96 would read "60.0s".
  if (Math.round(seconds * 10) / 10 < 60) return `${seconds.toFixed(1)}s`;
  // Rounded once, then split. Rounding the seconds part on its own printed
  // 119.6 as "1m 60s".
  const total = Math.round(seconds);
  const minutes = Math.floor(total / 60);
  if (minutes < 60) return `${minutes}m ${total % 60}s`;
  return `${Math.floor(minutes / 60)}h ${minutes % 60}m`;
}

async function load(): Promise<void> {
  if (refreshing.value) return;
  refreshing.value = true;
  error.value = '';
  try {
    snapshot.value = await adminApi.resources();
  } catch (failure) {
    error.value = failure instanceof Error ? failure.message : String(failure);
  } finally {
    refreshing.value = false;
  }
}

// Manual and automatic refresh share the same in-flight guard and keep the
// table mounted, including its page and sort order, if a sample fails.
// Paused while the tab is hidden, as the usage page is, and caught up on return.
const visibility = useDocumentVisibility();
useIntervalFn(() => { if (visibility.value !== 'hidden') void load(); }, REFRESH_MS);
watch(visibility, (now, was) => { if (now === 'visible' && was === 'hidden') void load(); });

onMounted(load);
</script>

<template>
  <Teleport :to="view.actionsHost">
    <button type="button" class="oa-btn" :disabled="refreshing" @click="load">{{ refreshing ? t('loading') : t('refresh') }}</button>
  </Teleport>
  <AdminFailure v-if="error && !snapshot" :message="error" @retry="load" />
  <p v-else-if="!snapshot" class="oa-table-empty">{{ t('loading') }}</p>
  <div v-else class="oa-resource-page">
    <div class="oa-resource-status"><span><span class="oa-dashboard-dot" />{{ t('controlAutoRefresh') }}</span><span>{{ t('controlResourceUpdated', { time: updatedAt }) }}</span></div>
    <p v-if="error" class="oa-drawer-flash visible" role="alert">{{ error }}</p>
    <div class="oa-resource-metrics">
      <div class="oa-resource-metric"><div class="oa-resource-metric-head"><span>{{ t('resStorage') }}</span><IconFile :size="20" /></div>
        <strong>{{ formatBytes(snapshot.storage.held_bytes) }}</strong><p>{{ t('resHeldNote') }}</p></div>
      <div class="oa-resource-metric"><div class="oa-resource-metric-head"><span>{{ t('resProcessMemory') }}</span><IconLayers :size="20" /></div>
        <strong>{{ formatBytes(snapshot.memory.sys_bytes) }}</strong><p>{{ t('resHeap') }} · {{ formatBytes(snapshot.memory.heap_bytes) }}</p></div>
      <div class="oa-resource-metric"><div class="oa-resource-metric-head"><span>{{ t('resCPUShare') }}</span><IconCpu :size="20" /></div>
        <strong>{{ cpuStats[0]?.value }}</strong><p>{{ cpuStats[0]?.note }}</p></div>
    </div>
    <div class="oa-resource-layout">
      <AdminControlCard id="resStorage" :title="t('resStorage')" :hint="t('controlStorageHint')" :icon="IconFile">
        <div class="oa-resource-storage-totals">
          <div><span>{{ t('resFiles') }}</span><strong>{{ snapshot.storage.held_count }}</strong></div>
          <div><span>{{ t('resDiscarded') }}</span><strong>{{ snapshot.storage.discarded_count }}</strong></div>
        </div>
        <p class="oa-field-hint">{{ t('resDiscardedNote') }}</p>
        <OaTable :columns="columns" :rows="snapshot.storage.by_user" :empty="t('resNoFiles')" :sort="sort" @sort="sort = $event">
          <template #cell-account="{ row }"><div class="oa-resource-account"><span class="oa-resource-avatar" aria-hidden="true">{{ isMasked('users') ? '*' : (Array.from(row.name)[0]?.toUpperCase() || '—') }}</span><span>{{ maskUser(row.name) }}</span></div></template>
          <template #cell-held="{ row }"><div class="oa-resource-share"><span>{{ formatBytes(row.bytes) }}</span>
            <div class="oa-health-meter" :title="t('controlStorageShare')" aria-hidden="true"><span :style="{ width: `${snapshot.storage.held_bytes > 0 ? Math.min(100, row.bytes / snapshot.storage.held_bytes * 100) : 0}%` }" /></div></div></template>
        </OaTable>
      </AdminControlCard>
      <div class="oa-resource-details">
        <AdminControlCard id="resMemory" :title="t('resMemory')" :icon="IconLayers">
          <dl class="oa-resource-definition"><div v-for="(stat, index) in memoryStats" :key="stat.label" :id="index === 2 ? 'resGoroutines' : undefined">
            <dt>{{ stat.label }}<small v-if="stat.note">{{ stat.note }}</small></dt><dd>{{ stat.value }}</dd>
          </div></dl>
        </AdminControlCard>
        <AdminControlCard id="resCPU" :title="t('resCPU')" :icon="IconCpu">
          <dl class="oa-resource-definition"><div v-for="stat in cpuStats.slice(1)" :key="stat.label">
            <dt>{{ stat.label }}<small v-if="stat.note">{{ stat.note }}</small></dt><dd>{{ stat.value }}</dd>
          </div></dl>
        </AdminControlCard>
      </div>
    </div>
    <p class="oa-field-hint">{{ t('resNoDatabaseSize') }}</p>
  </div>
</template>
