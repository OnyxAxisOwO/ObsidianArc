<script setup lang="ts">
// Everyone's allowance in one list: how much of each window every account has
// left, the tightest first. The account panel answers "why can this person
// not send anything"; this answers "who is about to be unable to".
//
// It is the one block on the usage page that does not follow the time range.
// An allowance is a position — what is left now — and not a total over a
// span, so it loads for itself and ignores the range and the model filter.
// Only the group filter reaches it, because "this group's accounts" means the
// same thing here as on the rest of the page.
//
// The list is the server's, sorted across everyone there is; the rows arrive
// a page at a time as the scrolling reaches the end, so a long list is as
// cheap to open as a short one.

import { computed, onBeforeUnmount, onMounted, ref, watch } from 'vue';
import { useDebounceFn } from '@vueuse/core';
import { adminApi, type AllowanceRow, type GroupOption } from '@/admin/api';
import OaScrollArea from '@/components/OaScrollArea.vue';
import OaUsageWindow from '@/components/OaUsageWindow.vue';
import { t } from '@/composables/useI18n';
import { isMasked, maskUser } from '@/admin/safeMode';
import type { UsageDisplay } from '@/api/usage';
import { useScrollFade } from './useScrollFade';

const props = defineProps<{ groupId: string }>();
const emit = defineEmits<{ (event: 'select', userId: string): void }>();

type State = '' | 'low' | 'exhausted';
const PAGE = 50;
// Rows shown before the list scrolls: five of these are as tall as the card
// beside it is useful.
const ROWS = 6;

const rows = ref<AllowanceRow[]>([]);
const total = ref(0);
const low = ref(0);
const exhausted = ref(0);
const display = ref<UsageDisplay>('absolute');
const groups = ref<GroupOption[]>([]);
const search = ref('');
const state = ref<State>('');
const loading = ref(false);
const failed = ref(false);

const states = computed<Array<{ value: State; label: string }>>(() => [
  { value: '', label: t('allowanceAll') },
  { value: 'low', label: `${t('allowanceLow')} ${low.value}` },
  { value: 'exhausted', label: `${t('allowanceExhausted')} ${exhausted.value}` },
]);

const overflows = computed(() => total.value > ROWS);
const { onScroll: fade, reset: resetFade, scrollClass } = useScrollFade(overflows);

// Answers can come back in a different order from the questions that asked
// for them, and the one that matters is the last one asked.
let ticket = 0;
let disposed = false;

function query(offset: number): string {
  const params = new URLSearchParams({ limit: String(PAGE), offset: String(offset) });
  if (search.value.trim()) params.set('q', search.value.trim());
  if (props.groupId) params.set('group_id', props.groupId);
  if (state.value) params.set('state', state.value);
  return params.toString();
}

async function load(append: boolean): Promise<void> {
  const mine = ++ticket;
  loading.value = true;
  try {
    const answer = await adminApi.usageAllowances(query(append ? rows.value.length : 0));
    if (disposed || mine !== ticket) return;
    if (append) {
      const seen = new Set(rows.value.map((row) => row.id));
      rows.value = [...rows.value, ...(answer.rows ?? []).filter((row) => !seen.has(row.id))];
    } else {
      rows.value = answer.rows ?? [];
      resetFade();
    }
    total.value = answer.total ?? 0;
    low.value = answer.low ?? 0;
    exhausted.value = answer.exhausted ?? 0;
    display.value = answer.display ?? 'absolute';
    failed.value = false;
  } catch {
    if (!disposed && mine === ticket) failed.value = true;
  } finally {
    if (!disposed && mine === ticket) loading.value = false;
  }
}

const reload = useDebounceFn(() => { void load(false); }, 300);
watch(search, reload);
watch(() => props.groupId, () => { void load(false); });
function pick(next: State): void {
  if (state.value === next) return;
  state.value = next;
  void load(false);
}

onMounted(() => {
  void load(false);
  void adminApi.groupOptions().then((answer) => { if (!disposed) groups.value = answer.groups; }).catch(() => {});
});
onBeforeUnmount(() => { disposed = true; });

function onScroll(event: Event): void {
  fade(event);
  const element = event.target as HTMLElement;
  const nearEnd = element.scrollTop + element.clientHeight >= element.scrollHeight - 160;
  if (nearEnd && !loading.value && rows.value.length < total.value) void load(true);
}

function name(row: AllowanceRow): string {
  return maskUser(row.nickname || row.username);
}
function initial(row: AllowanceRow): string {
  if (isMasked('users')) return '*';
  return Array.from(row.nickname || row.username)[0]?.toLocaleUpperCase() ?? '·';
}
function groupName(id: string): string {
  return groups.value.find((group) => group.id === id)?.name ?? '';
}
function sub(row: AllowanceRow): string {
  return [`@${maskUser(row.username)}`, groupName(row.group_id)].filter(Boolean).join(' · ');
}
</script>

<template>
  <section id="secAllowances" class="oa-viz-card">
    <header class="oa-viz-head">
      <div><span class="oa-kicker">{{ t('allowanceOverview') }}</span><h2>{{ t('allowanceTitle') }}</h2></div>
      <div class="oa-segment" role="group" :aria-label="t('allowanceTitle')">
        <button v-for="option in states" :key="option.value" type="button" :aria-pressed="state === option.value"
          @click="pick(option.value)">{{ option.label }}</button>
      </div>
    </header>
    <div class="oa-filters oa-allow-search">
      <input v-model="search" type="search" :placeholder="t('allowanceSearch')" :aria-label="t('allowanceSearch')">
    </div>

    <p v-if="failed && !rows.length" class="oa-board-empty">{{ t('couldNotLoad') }}</p>
    <p v-else-if="!rows.length && !loading" class="oa-board-empty">{{ t('allowanceEmpty') }}</p>
    <div v-else class="oa-board-frame oa-allow-frame" :style="{ '--board-rows': ROWS }">
      <OaScrollArea wrap-class="oa-board-wrap" :scroll-class="scrollClass" @scroll="onScroll">
        <ol class="oa-board oa-allow">
          <li v-for="row in rows" :key="row.id">
            <button type="button" class="oa-allow-row" :title="t('boardDrill', { name: name(row) })" @click="emit('select', row.id)">
              <span class="oa-board-mark oa-allow-mark" aria-hidden="true">{{ initial(row) }}</span>
              <span class="oa-allow-who">
                <span class="oa-board-name">{{ name(row) }}</span>
                <span class="oa-allow-sub">{{ sub(row) }}</span>
              </span>
              <span class="oa-allow-windows">
                <template v-for="window in row.windows" :key="window.kind">
                  <OaUsageWindow v-if="window.enforced" :window="window" :display="display" :masked="isMasked('billing')" />
                  <div v-else class="oa-allow-none">
                    <span>{{ t(window.kind === '5h' ? 'quota5h' : window.kind === '1w' ? 'quotaWeek' : 'quotaMonth') }}</span>
                    <span>{{ t('allowanceNoLimit') }}</span>
                  </div>
                </template>
              </span>
            </button>
          </li>
        </ol>
      </OaScrollArea>
      <p v-if="total > ROWS" class="oa-board-count">{{ t('boardCount', { count: total }) }}</p>
    </div>
  </section>
</template>
