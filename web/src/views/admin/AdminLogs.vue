<script setup lang="ts">
// The request log: everything the server answered, filterable.
//
// Separate from the usage screen, which is about spend. This one answers
// "what happened" — and the requests worth looking for are usually the ones
// that cost nothing because they were refused, which never reach the ledger
// at all.
//
// The filter controls are built from what is actually in the log rather than
// from every value that could theoretically appear, so an operator picks from
// a list of things that happened. That also means they are rebuilt when the
// window changes: last hour and last month have different casts.

import { computed, onMounted, ref, watch } from 'vue';
import { useRoute, useRouter } from 'vue-router';
import { adminApi, type LogEntry, type LogFacets, type LogOption } from '@/admin/api';
import { ApiError } from '@/api/client';
import OaPagination from '@/components/OaPagination.vue';
import type { PageState } from '@/components/table-types';
import OaBadge from '@/components/OaBadge.vue';
import OaIconButton from '@/components/OaIconButton.vue';
import OaPanel from '@/components/OaPanel.vue';
import OaSelectField from '@/components/OaSelectField.vue';
import OaTextField from '@/components/OaTextField.vue';
import type { Choice } from '@/components/choice';
import { RefreshCw } from 'lucide-vue-next';
import { t, type StringKey } from '@/composables/useI18n';
import { absoluteTime, relativeTime } from '@/lib/format';
import { rememberedPageSize } from '@/lib/page-size';
import { maskUser, maskLog } from '@/admin/safeMode';
import AdminFailure from './AdminFailure.vue';
import { useAdminView } from './adminView';

const pageSize = ref(rememberedPageSize());

/**
 * Windows offered for "since". Empty is everything, which is the point of a
 * log that is never pruned.
 */
const WINDOWS: Array<{ value: string; label: StringKey; hours: number }> = [
  { value: '1', label: 'lastHour', hours: 1 },
  { value: '24', label: 'last24h', hours: 24 },
  { value: '168', label: 'last7d', hours: 168 },
  { value: '720', label: 'last30d', hours: 720 },
  { value: '', label: 'allTime', hours: 0 },
];

const view = useAdminView();
view.setTitle(t('navLogs'), t('logsSubtitle'));

const router = useRouter();
const route = useRoute();

function parseRouteQuery() {
  return {
    window: typeof route.query.window === 'string' ? route.query.window : '24',
    userID: typeof route.query.user_id === 'string' ? route.query.user_id : '',
    modelID: typeof route.query.model_id === 'string' ? route.query.model_id : '',
    outcome: typeof route.query.outcome === 'string' ? route.query.outcome : '',
    status: typeof route.query.status === 'string' ? route.query.status : '',
    channel: typeof route.query.channel === 'string' ? route.query.channel : '',
    errorCode: typeof route.query.error_code === 'string' ? route.query.error_code : '',
    path: typeof route.query.path === 'string' ? route.query.path : '',
    offset: 0,
  };
}

const query = ref(parseRouteQuery());

watch(
  () => route.query,
  () => {
    query.value = parseRouteQuery();
    void reload();
  },
);

const facets = ref<LogFacets | null>(null);
const entries = ref<LogEntry[]>([]);
const total = ref(0);
const offset = ref(0);
const error = ref('');
const listError = ref('');
const loading = ref(true);
const opened = ref<LogEntry | null>(null);
let listRequest = 0;

function since(): number {
  const entry = WINDOWS.find((window) => window.value === query.value.window);
  if (!entry || entry.hours === 0) return 0;
  return Date.now() - entry.hours * 60 * 60 * 1000;
}

function listQuery(): string {
  const params = new URLSearchParams();
  const at = since();
  if (at > 0) params.set('since', String(at));
  if (query.value.userID) params.set('user_id', query.value.userID);
  if (query.value.modelID) params.set('model_id', query.value.modelID);
  if (query.value.outcome) params.set('outcome', query.value.outcome);
  if (query.value.status) params.set('status', query.value.status);
  if (query.value.channel) params.set('channel', query.value.channel);
  if (query.value.errorCode) params.set('error_code', query.value.errorCode);
  if (query.value.path) params.set('path', query.value.path);
  params.set('limit', String(pageSize.value));
  params.set('offset', String(query.value.offset));
  return `?${params.toString()}`;
}

function withAny(options: LogOption[], anyLabel: string, fallbackValue?: string, maskFn?: (s: string) => string): Array<Choice<string>> {
  const choices: Array<Choice<string>> = [
    { value: '', label: anyLabel },
    ...options.map((option) => ({
      value: option.value,
      label: `${maskFn ? maskFn(option.label) : option.label} (${option.count})`,
    })),
  ];
  if (fallbackValue && !choices.some((c) => c.value === fallbackValue)) {
    choices.push({ value: fallbackValue, label: maskFn ? maskFn(fallbackValue) : fallbackValue });
  }
  return choices;
}

/** Any change to what is being asked invalidates which page we are on. */
function narrow(): void {
  query.value.offset = 0;
  void paint();
}

async function reload(): Promise<void> {
  error.value = '';
  try {
    // The facets follow the window: which accounts and models appear in the
    // last hour is a different list from the last month's.
    const at = since();
    facets.value = await adminApi.logFacets(at > 0 ? `?since=${at}` : '');
  } catch (failure) {
    error.value = failure instanceof Error ? failure.message : String(failure);
    return;
  }
  await paint();
}

async function paint(): Promise<void> {
  const ticket = ++listRequest;
  loading.value = true;
  listError.value = '';
  try {
    const data = await adminApi.logs(listQuery());
    if (ticket !== listRequest) return;
    entries.value = data.entries;
    total.value = data.total;
    offset.value = data.offset;
  } catch (failure) {
    if (ticket !== listRequest) return;
    entries.value = [];
    listError.value = failure instanceof ApiError ? failure.message : t('failed');
  } finally {
    if (ticket === listRequest) loading.value = false;
  }
}

function clearFilters(): void {
  query.value = {
    ...query.value,
    userID: '', modelID: '', outcome: '', status: '',
    channel: '', errorCode: '', path: '', offset: 0,
  };
  if (Object.keys(route.query).length > 0) {
    void router.replace({ path: '/admin/logs', query: {} });
  }
  void paint();
}

function page(next: PageState): void {
  pageSize.value = next.pageSize;
  query.value.offset = (next.page - 1) * next.pageSize;
  void paint();
}


function tone(status: number): 'default' | 'muted' | 'danger' {
  return status >= 500 ? 'danger' : status >= 400 ? 'muted' : 'default';
}

const facts = computed<Array<[string, string]>>(() => {
  const entry = opened.value;
  if (!entry) return [];
  return [
    [t('logStatus'), String(entry.status)],
    [t('logWhen'), absoluteTime(entry.at)],
    [t('logDuration'), `${entry.duration_ms} ms`],
    [t('logBytes'), String(entry.bytes)],
    [t('logUser'), entry.username ? maskUser(entry.username) : t('logAnonymous')],
    [t('logChannel'), entry.channel || '—'],
    [t('logModel'), entry.model_name || '—'],
    [t('logErrorCode'), entry.error_code || '—'],
    [t('logIP'), entry.ip ? maskLog(entry.ip) : '—'],
    [t('logRequestID'), entry.request_id || '—'],
    [t('logUserAgent'), entry.user_agent ? maskLog(entry.user_agent) : '—'],
  ];
});

onMounted(reload);
</script>

<template>
  <Teleport :to="view.actionsHost">
    <!-- A refresh icon, which it had not been: the button was a chevron, and
         read as a control that collapsed something. -->
    <OaIconButton class="oa-icon-btn" :label="t('refresh')" :disabled="loading" @click="reload">
      <RefreshCw :size="15" :class="{ 'is-refreshing': loading }" aria-hidden="true" />
    </OaIconButton>
  </Teleport>

  <AdminFailure v-if="error" :message="error" @retry="reload" />

  <div v-else class="oa-log-page">
    <div v-if="facets" id="logsFilter" class="oa-viz-card oa-log-filters">
      <div class="oa-log-filter-grid">
        <OaSelectField
          v-model="query.window"
          :label="t('logWindow')"
          :options="WINDOWS.map((entry) => ({ value: entry.value, label: t(entry.label) }))"
          @update:model-value="query.offset = 0; reload()"
        />
        <OaSelectField
          v-model="query.outcome"
          :label="t('logOutcome')"
          :options="[
            { value: '', label: t('logAnyOutcome') },
            { value: 'ok', label: t('logSucceeded') },
            { value: 'failed', label: t('logFailed') },
          ]"
          @update:model-value="narrow"
        />
        <OaSelectField
          v-model="query.userID"
          searchable
          :label="t('logUser')"
          :options="withAny(facets.users, t('logAnyUser'), query.userID, maskUser)"
          @update:model-value="narrow"
        />
        <OaSelectField
          v-model="query.modelID"
          searchable
          :label="t('logModel')"
          :options="withAny(facets.models, t('logAnyModel'), query.modelID)"
          @update:model-value="narrow"
        />
        <OaSelectField
          v-model="query.status"
          :label="t('logStatus')"
          :options="withAny(facets.statuses, t('logAnyStatus'), query.status)"
          @update:model-value="narrow"
        />
        <OaSelectField
          v-model="query.errorCode"
          :label="t('logErrorCode')"
          :options="withAny(facets.error_codes, t('logAnyErrorCode'), query.errorCode)"
          @update:model-value="narrow"
        />
        <OaSelectField
          v-model="query.channel"
          :label="t('logChannel')"
          :options="[
            { value: '', label: t('logAnyChannel') },
            { value: 'web', label: t('logChannelWeb') },
            { value: 'api', label: t('logChannelAPI') },
          ]"
          @update:model-value="narrow"
        />
        <OaTextField v-model="query.path" :label="t('logPath')" placeholder="/api/chat" />
      </div>

      <div class="oa-log-filter-actions">
        <button type="button" class="oa-btn primary" @click="narrow">{{ t('logApply') }}</button>
        <button type="button" class="oa-btn" @click="clearFilters">{{ t('logClear') }}</button>
        <span class="oa-log-holding">
          <!-- A gap in an audit trail has to be visible, not inferred. -->
          <OaBadge v-if="facets.dropped > 0" tone="danger">
            {{ t('logDropped', { count: facets.dropped }) }}
          </OaBadge>
          {{ t('logHolding', { count: facets.total }) }}
        </span>
      </div>
    </div>

    <!-- Columns rather than two stacked lines per row: the questions asked of
         a log — which ones failed, whose, how slow — are asked down a column,
         and a column is only readable when every row puts the same thing in
         the same place. -->
    <div id="logsTable" class="oa-viz-card oa-log-results">
      <p v-if="loading && !entries.length" class="oa-table-empty">{{ t('loading') }}</p>
      <p v-else-if="listError" class="oa-table-empty">{{ listError }}</p>
      <p v-else-if="!entries.length" class="oa-table-empty">{{ t('logEmpty') }}</p>

      <template v-else>
        <div class="oa-log-list" :aria-busy="loading">
          <div class="oa-log-head" aria-hidden="true">
            <span>{{ t('logStatus') }}</span>
            <span>{{ t('logRequest') }}</span>
            <span>{{ t('logUser') }}</span>
            <span>{{ t('logModel') }}</span>
            <span class="numeric">{{ t('logDuration') }}</span>
            <span class="numeric">{{ t('logWhen') }}</span>
          </div>
          <button
            v-for="entry in entries"
            :key="entry.id"
            type="button"
            class="oa-log-row"
            @click="opened = entry"
          >
            <span><OaBadge :tone="tone(entry.status)">{{ entry.status }}</OaBadge></span>
            <span class="oa-log-request">
              <span class="oa-log-method">{{ entry.method }}</span>
              <span class="oa-log-path">{{ entry.path }}</span>
              <span v-if="entry.error_code" class="oa-log-error">{{ entry.error_code }}</span>
            </span>
            <span class="oa-log-cell" :class="{ quiet: !entry.username }">{{ entry.username ? maskUser(entry.username) : t('logAnonymous') }}</span>
            <span class="oa-log-cell" :class="{ quiet: !entry.model_name }">{{ entry.model_name || '—' }}</span>
            <span class="oa-log-cell numeric">{{ entry.duration_ms }} ms</span>
            <span class="oa-log-cell numeric quiet" :title="absoluteTime(entry.at)">{{ relativeTime(entry.at) }}</span>
          </button>
        </div>
      </template>
      <OaPagination :page="Math.floor(query.offset / pageSize) + 1" :page-size="pageSize" :total="total" :busy="loading" @change="page" />
    </div>
  </div>

  <!-- The whole record, for the one request somebody is actually asking about. -->
  <OaPanel
    v-if="opened"
    :title="`${opened.method} ${opened.path}`"
    :footer="false"
    :width="460"
    @close="opened = null"
  >
    <dl class="oa-log-facts">
      <template v-for="[label, value] in facts" :key="label">
        <dt>{{ label }}</dt>
        <dd>{{ value }}</dd>
      </template>
    </dl>
  </OaPanel>
</template>
