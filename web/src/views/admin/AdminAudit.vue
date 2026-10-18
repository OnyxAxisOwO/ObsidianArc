<script setup lang="ts">
// The administrators' audit trail: who did what here, newest first.
//
// Read as a diary rather than as a log. The request log is asked questions
// down a column — which failed, how slow — so it is a table; this one is
// asked "what happened on Tuesday" and "what did this person do", so it is a
// list of sentences under the day they happened, and the columns live in the
// record a click away.

import { computed, onMounted, ref, watch } from 'vue';
import { useRoute, useRouter } from 'vue-router';
import { RefreshCw, X } from 'lucide-vue-next';
import { adminApi, type AuditEntry, type AuditFacets } from '@/admin/api';
import { AUDIT_ACTIONS, auditWeight } from './audit-actions';
import { auditText as at, type AuditTextKey } from './audit-text';
import { maskLog, maskUser } from '@/admin/safeMode';
import { ApiError } from '@/api/client';
import OaBadge from '@/components/OaBadge.vue';
import OaIconButton from '@/components/OaIconButton.vue';
import OaPagination from '@/components/OaPagination.vue';
import OaPanel from '@/components/OaPanel.vue';
import OaSelectField from '@/components/OaSelectField.vue';
import OaSelect from '@/components/OaSelect.vue';
import type { Choice } from '@/components/choice';
import type { PageState } from '@/components/table-types';
import { BACKOFFICE_PANEL_PARAM } from '@/composables/usePanelExit';
import { t, type StringKey } from '@/composables/useI18n';
import { absoluteTime } from '@/lib/format';
import { rememberedPageSize } from '@/lib/page-size';
import { isSuperAdmin } from '@/stores/session';
import AdminFailure from './AdminFailure.vue';
import { useAdminView } from './adminView';

const WINDOWS: Array<{ value: string; label: StringKey; hours: number }> = [
  { value: '24', label: 'last24h', hours: 24 },
  { value: '168', label: 'last7d', hours: 168 },
  { value: '720', label: 'last30d', hours: 720 },
  { value: '', label: 'allTime', hours: 0 },
];

// The steps a retention is chosen from. The server takes any number in its
// bounds; a list keeps the choice to the ones anybody means.
const RETENTIONS = [30, 90, 180, 365, 730, 1825, 3650];

const view = useAdminView();
view.setTitle(() => t('navAudit'), () => t('auditSubtitle'));

const route = useRoute();
const router = useRouter();
const pageSize = ref(rememberedPageSize());

function fromAddress() {
  const read = (name: string) => (typeof route.query[name] === 'string' ? (route.query[name] as string) : '');
  // All time is the empty value and is written as `?window=`, so only an
  // absent or unknown window falls back to the default; reading the empty
  // one as missing put the control back on seven days the moment it was
  // chosen.
  const windowed = route.query['window'];
  const window = typeof windowed === 'string' && WINDOWS.some((entry) => entry.value === windowed) ? windowed : '168';
  return {
    window,
    actorID: read('actor_id'),
    action: read('action'),
    targetID: read('target_id'),
    targetName: read('target'),
    outcome: read('outcome'),
    via: read('via'),
    offset: 0,
  };
}

const query = ref(fromAddress());

// A panel opened beside the page is named in the address too; only the
// filters are read back from it, so opening one neither resets them nor
// refetches the trail.
//
// The facets are counted over the window alone, so a change to any other
// filter repaints the list without asking for them again. A page showing a
// failure, or still without facets, reloads whole so the failure can clear.
watch(
  () => JSON.stringify(fromAddress()),
  (next, previous) => {
    const windowMoved = (JSON.parse(next) as { window: string }).window
      !== (JSON.parse(previous) as { window: string }).window;
    query.value = fromAddress();
    void (windowMoved || error.value || !facets.value ? reload() : paint());
  },
);

const facets = ref<AuditFacets | null>(null);
const entries = ref<AuditEntry[]>([]);
const total = ref(0);
const error = ref('');
const listError = ref('');
const loading = ref(true);
let listTicket = 0;
let facetTicket = 0;

function since(): number {
  const entry = WINDOWS.find((window) => window.value === query.value.window);
  return entry && entry.hours > 0 ? Date.now() - entry.hours * 3_600_000 : 0;
}

function listQuery(): string {
  const params = new URLSearchParams();
  const at = since();
  if (at > 0) params.set('since', String(at));
  if (query.value.actorID) params.set('actor_id', query.value.actorID);
  if (query.value.action) params.set('action', query.value.action);
  if (query.value.targetID) params.set('target_id', query.value.targetID);
  if (query.value.outcome) params.set('outcome', query.value.outcome);
  if (query.value.via) params.set('via', query.value.via);
  params.set('limit', String(pageSize.value));
  params.set('offset', String(query.value.offset));
  return `?${params.toString()}`;
}

async function reload(): Promise<void> {
  error.value = '';
  const ticket = ++facetTicket;
  try {
    const at = since();
    const next = await adminApi.auditFacets(at > 0 ? `?since=${at}` : '');
    if (ticket !== facetTicket) return;
    facets.value = next;
  } catch (failure) {
    if (ticket !== facetTicket) return;
    error.value = failure instanceof Error ? failure.message : String(failure);
    return;
  }
  await paint();
}

async function paint(): Promise<void> {
  const ticket = ++listTicket;
  loading.value = true;
  listError.value = '';
  try {
    const data = await adminApi.audit(listQuery());
    if (ticket !== listTicket) return;
    // A page past the end, because the list shrank under it (a row deleted,
    // a sliding window moving on), is asked again for its last page rather
    // than left empty: with one page left the pager shows no way back.
    if (data.total > 0 && query.value.offset >= data.total) {
      query.value.offset = Math.floor((data.total - 1) / pageSize.value) * pageSize.value;
      await paint(); return;
    }
    entries.value = data.entries;
    total.value = data.total;
  } catch (failure) {
    if (ticket !== listTicket) return;
    entries.value = [];
    listError.value = failure instanceof ApiError ? failure.message : t('failed');
  } finally {
    if (ticket === listTicket) loading.value = false;
  }
}

/** Writes the filters into the address, keeping a panel open beside the page. */
function remember(): void {
  const keep: Record<string, string> = {};
  const panel = route.query[BACKOFFICE_PANEL_PARAM];
  if (typeof panel === 'string') keep[BACKOFFICE_PANEL_PARAM] = panel;
  const q = query.value;
  if (q.window !== '168') keep.window = q.window;
  if (q.actorID) keep.actor_id = q.actorID;
  if (q.action) keep.action = q.action;
  if (q.targetID) {
    keep.target_id = q.targetID;
    if (q.targetName) keep.target = q.targetName;
  }
  if (q.outcome) keep.outcome = q.outcome;
  if (q.via) keep.via = q.via;
  void router.replace({ path: '/admin/audit', query: keep });
}

function clearFilters(): void {
  query.value = { ...query.value, actorID: '', action: '', targetID: '', targetName: '', outcome: '', via: '', offset: 0 };
  remember();
  // Kept although the replace may reload through the watch: a Clear that
  // changes nothing in the address would not fire it.
  void paint();
}

function page(next: PageState): void {
  pageSize.value = next.pageSize;
  query.value.offset = (next.page - 1) * next.pageSize;
  void paint();
}

// --- words --------------------------------------------------------------------

function actionKey(entry: Pick<AuditEntry, 'method' | 'route'>): string {
  return `${entry.method} ${entry.route}`;
}

function actionLabel(entry: Pick<AuditEntry, 'method' | 'route' | 'path'>): string {
  const key = AUDIT_ACTIONS[actionKey(entry)];
  return key ? at(key) : `${entry.method} ${entry.path}`;
}

function actionOptionLabel(value: string): string {
  const key = AUDIT_ACTIONS[value];
  return key ? at(key) : value;
}

const VIA: Record<string, AuditTextKey> = {
  web: 'viaWeb',
  'console:web': 'viaConsoleWeb',
  'console:ssh': 'viaConsoleSSH',
};

function viaLabel(via: string): string {
  const key = VIA[via];
  return key ? at(key) : via;
}

function roleLabel(role: string): string {
  if (role === 'super_admin') return at('roleSuperAdmin');
  if (role === 'admin') return t('roleAdmin');
  return role || '—';
}

/**
 * The target as a row shows it. A thing with no name left — a model deleted
 * before the trail could look it up — is its id, cut to where two ids
 * already differ; the whole id is in the record.
 */
function targetShort(entry: AuditEntry): string {
  if (!entry.target_label && entry.target_id.length > 12) return `${entry.target_id.slice(0, 10)}…`;
  return targetName(entry);
}

/** Accounts are masked in safe mode; everything else is named as stored. */
function targetName(entry: AuditEntry): string {
  const name = entry.target_label || entry.target_id;
  if (!name) return '';
  return entry.route.startsWith('/api/admin/users/{id}') ? maskUser(name) : name;
}

function outcome(entry: AuditEntry): { tone: 'danger' | 'warning'; text: string } | null {
  if (entry.status < 400) return null;
  if (entry.status === 401 || entry.status === 403) return { tone: 'warning', text: at('refused') };
  return { tone: 'danger', text: at('failedStatus', { status: entry.status }) };
}

function initial(name: string): string {
  return (name.trim()[0] ?? '?').toUpperCase();
}

const timeFormat: Intl.DateTimeFormatOptions = { hour: '2-digit', minute: '2-digit' };

function dayKey(at: number): string {
  const d = new Date(at);
  return `${d.getFullYear()}-${d.getMonth()}-${d.getDate()}`;
}

function dayLabel(at: number): string {
  const today = new Date();
  const yesterday = new Date(today.getFullYear(), today.getMonth(), today.getDate() - 1);
  if (dayKey(at) === dayKey(today.getTime())) return t('presetToday');
  if (dayKey(at) === dayKey(yesterday.getTime())) return t('presetYesterday');
  const d = new Date(at);
  return d.toLocaleDateString(undefined, {
    weekday: 'short', month: 'long', day: 'numeric',
    ...(d.getFullYear() === today.getFullYear() ? {} : { year: 'numeric' }),
  });
}

const days = computed(() => {
  const out: Array<{ key: string; label: string; entries: AuditEntry[] }> = [];
  for (const entry of entries.value) {
    const key = dayKey(entry.at);
    const last = out[out.length - 1];
    if (last && last.key === key) last.entries.push(entry);
    else out.push({ key, label: dayLabel(entry.at), entries: [entry] });
  }
  return out;
});

function showFailed(): void {
  query.value = { ...query.value, outcome: 'failed', offset: 0 };
  remember();
}

function withAny(options: Array<{ value: string; label: string; count: number }>, any: string, current: string,
  label: (option: { value: string; label: string }) => string): Array<Choice<string>> {
  const choices: Array<Choice<string>> = [
    { value: '', label: any },
    ...options.map((option) => ({ value: option.value, label: `${label(option)} (${option.count})` })),
  ];
  if (current && !choices.some((choice) => choice.value === current)) {
    choices.push({ value: current, label: label({ value: current, label: current }) });
  }
  return choices;
}

const actorChoices = computed(() =>
  withAny(facets.value?.actors ?? [], at('anyActor'), query.value.actorID, (o) => maskUser(o.label)));
const actionChoices = computed(() =>
  withAny(facets.value?.routes ?? [], at('anyAction'), query.value.action, (o) => actionOptionLabel(o.value)));

// --- one record ---------------------------------------------------------------

const opened = ref<AuditEntry | null>(null);
const detailError = ref('');
let detailTicket = 0;

async function open(entry: AuditEntry): Promise<void> {
  opened.value = entry;
  detailError.value = '';
  const ticket = ++detailTicket;
  try {
    const { entry: full } = await adminApi.auditEntry(entry.id);
    if (ticket === detailTicket) opened.value = full;
  } catch (failure) {
    if (ticket === detailTicket) detailError.value = failure instanceof ApiError ? failure.message : t('failed');
  }
}

const facts = computed<Array<[string, string]>>(() => {
  const e = opened.value;
  if (!e) return [];
  return [
    [at('when'), absoluteTime(e.at)],
    [at('actor'), maskUser(e.actor_username) || '—'],
    [t('role'), roleLabel(e.actor_role)],
    [at('path'), `${e.method} ${e.path}`],
    [at('target'), e.target_label ? `${targetName(e)} · ${e.target_id}` : (e.target_id || '—')],
    [at('status'), String(e.status)],
    [at('duration'), `${e.duration_ms} ms`],
    [at('via'), viaLabel(e.via)],
    [at('iP'), e.ip ? maskLog(e.ip) : '—'],
    [at('userAgent'), e.user_agent ? maskLog(e.user_agent) : '—'],
  ];
});

/** A field that holds a moment reads as one, not as thirteen digits. */
function changeValue(field: string, value: string): string {
  if (/(_at|_until)$/.test(field) && /^\d{11,}$/.test(value)) return absoluteTime(Number(value));
  if (/(_at|_until)$/.test(field) && value === '0') return '—';
  return value;
}

function showActor(entry: AuditEntry): void {
  query.value = { ...query.value, actorID: entry.actor_id, offset: 0 };
  opened.value = null;
  remember();
}

function showTarget(entry: AuditEntry): void {
  query.value = { ...query.value, targetID: entry.target_id, targetName: targetName(entry), offset: 0 };
  opened.value = null;
  remember();
}

function clearTarget(): void {
  query.value = { ...query.value, targetID: '', targetName: '', offset: 0 };
  remember();
}

// --- retention ----------------------------------------------------------------

const retentionChoices = computed<Array<Choice<string>>>(() => {
  const current = facets.value?.retention_days ?? 180;
  const steps = RETENTIONS.includes(current) ? RETENTIONS : [...RETENTIONS, current].sort((a, b) => a - b);
  return steps.map((days) => ({ value: String(days), label: at('retentionDays', { count: days }) }));
});
const retentionError = ref('');

async function saveRetention(value: string): Promise<void> {
  retentionError.value = '';
  try {
    await adminApi.saveSettings({ 'audit.retention_days': value });
    if (facets.value) facets.value = { ...facets.value, retention_days: Number(value) };
  } catch (failure) {
    retentionError.value = failure instanceof ApiError ? failure.message : t('failed');
  }
}

// Read through a computed so the template never sees the store's ref itself.
const superAdmin = computed(() => isSuperAdmin.value);

onMounted(reload);
</script>

<template>
  <Teleport :to="view.actionsHost">
    <OaIconButton class="oa-icon-btn" :label="t('refresh')" :disabled="loading" @click="reload">
      <RefreshCw :size="15" :class="{ 'is-refreshing': loading }" aria-hidden="true" />
    </OaIconButton>
  </Teleport>

  <AdminFailure v-if="error" :message="error" @retry="reload" />

  <div v-else class="oa-log-page oa-audit-page">
    <div v-if="facets" id="auditFilter" class="oa-viz-card oa-log-filters">
      <div class="oa-log-filter-grid">
        <OaSelectField
          v-model="query.window"
          :label="t('logWindow')"
          :options="WINDOWS.map((entry) => ({ value: entry.value, label: t(entry.label) }))"
          @update:model-value="query.offset = 0; remember()"
        />
        <OaSelectField
          v-model="query.actorID"
          searchable
          :label="at('actor')"
          :options="actorChoices"
          @update:model-value="query.offset = 0; remember()"
        />
        <OaSelectField
          v-model="query.action"
          searchable
          :label="at('action')"
          :options="actionChoices"
          @update:model-value="query.offset = 0; remember()"
        />
        <OaSelectField
          v-model="query.outcome"
          :label="t('logOutcome')"
          :options="[
            { value: '', label: t('logAnyOutcome') },
            { value: 'ok', label: t('logSucceeded') },
            { value: 'failed', label: t('logFailed') },
          ]"
          @update:model-value="query.offset = 0; remember()"
        />
      </div>

      <div class="oa-log-filter-actions">
        <div class="oa-segment oa-audit-vias" role="group" :aria-label="at('via')">
          <button
            v-for="via in ['', 'web', 'console:web', 'console:ssh']"
            :key="via"
            type="button"
            :aria-pressed="query.via === via"
            @click="query.via = via; query.offset = 0; remember()"
          >{{ via ? viaLabel(via) : at('anyVia') }}</button>
        </div>
        <button v-if="query.targetID" type="button" class="oa-badge oa-audit-badge-btn" :title="t('logClear')" @click="clearTarget">
          {{ at('onTarget', { name: query.targetName || query.targetID }) }}
          <X :size="12" aria-hidden="true" />
        </button>
        <button type="button" class="oa-btn" @click="clearFilters">{{ t('logClear') }}</button>
        <span class="oa-log-holding oa-audit-summary">
          <span>{{ at('summary', { total: facets.total.toLocaleString(), actors: facets.actors.length }) }}</span>
          <button v-if="facets.failed > 0" type="button" class="oa-badge oa-badge-danger oa-audit-badge-btn" @click="showFailed">
            {{ at('summaryFailed', { count: facets.failed.toLocaleString() }) }}
          </button>
          <!-- The super administrator's alone, so for anybody else it is a
               sentence rather than a control that would only refuse them. -->
          <label v-if="superAdmin" id="auditRetention" class="oa-audit-keep" :title="at('retentionHint')">
            {{ at('retentionLabel') }}
            <OaSelect
              class="oa-filter-select"
              :aria-label="at('retentionLabel')"
              :model-value="String(facets.retention_days)"
              :choices="retentionChoices"
              :searchable="false"
              @update:model-value="saveRetention"
            />
          </label>
          <span v-else id="auditRetention">{{ at('retention', { count: facets.retention_days }) }}</span>
          <OaBadge v-if="retentionError" tone="danger">{{ retentionError }}</OaBadge>
        </span>
      </div>
    </div>

    <div id="auditTrail" class="oa-viz-card oa-audit-results">
      <p v-if="loading && !entries.length" class="oa-table-empty">{{ t('loading') }}</p>
      <p v-else-if="listError" class="oa-table-empty">{{ listError }}</p>
      <p v-else-if="!entries.length" class="oa-table-empty">{{ at('empty') }}</p>

      <div v-else class="oa-audit-days" :aria-busy="loading" :class="{ busy: loading }">
        <section v-for="day in days" :key="day.key" class="oa-audit-day">
          <h3 class="oa-audit-day-label">{{ day.label }}</h3>
          <ol class="oa-audit-list">
            <li v-for="entry in day.entries" :key="entry.id">
              <button
                type="button"
                class="oa-audit-row"
                :class="{
                  'is-notable': auditWeight(entry.method, entry.route) === 'notable',
                  'is-failed': entry.status >= 400,
                }"
                @click="open(entry)"
              >
                <span class="oa-audit-avatar" aria-hidden="true">{{ initial(entry.actor_username) }}</span>
                <span class="oa-audit-body">
                  <span class="oa-audit-line">
                    <strong class="oa-audit-actor">{{ maskUser(entry.actor_username) }}</strong>
                    <span class="oa-audit-action">{{ actionLabel(entry) }}</span>
                    <OaBadge v-if="targetShort(entry)" tone="muted">{{ targetShort(entry) }}</OaBadge>
                    <OaBadge v-if="entry.via !== 'web'">{{ viaLabel(entry.via) }}</OaBadge>
                    <OaBadge v-if="outcome(entry)" :tone="outcome(entry)!.tone">{{ outcome(entry)!.text }}</OaBadge>
                  </span>
                </span>
                <span class="oa-audit-side">
                  <span v-if="entry.ip" class="oa-audit-ip">{{ maskLog(entry.ip) }}</span>
                  <time class="oa-audit-time" :datetime="new Date(entry.at).toISOString()" :title="absoluteTime(entry.at)">
                    {{ new Date(entry.at).toLocaleTimeString([], timeFormat) }}
                  </time>
                </span>
              </button>
            </li>
          </ol>
        </section>
      </div>
      <OaPagination :page="Math.floor(query.offset / pageSize) + 1" :page-size="pageSize" :total="total" :busy="loading" @change="page" />
    </div>

  </div>

  <OaPanel
    v-if="opened"
    :title="actionLabel(opened)"
    :footer="false"
    :width="480"
    :error="detailError"
    @close="opened = null"
  >
    <div class="oa-audit-detail">
      <OaBadge v-if="outcome(opened)" :tone="outcome(opened)!.tone">{{ outcome(opened)!.text }}</OaBadge>

      <dl class="oa-log-facts">
        <template v-for="[label, value] in facts" :key="label">
          <dt>{{ label }}</dt>
          <dd>{{ value }}</dd>
        </template>
      </dl>

      <section v-if="opened.changes?.length" class="oa-audit-section">
        <h4>{{ at('changes') }}</h4>
        <ul class="oa-audit-changes">
          <li v-for="(change, index) in opened.changes" :key="index">
            <span class="oa-audit-field">{{ change.field }}</span>
            <span v-if="change.secret" class="oa-audit-secret">{{ at('secretChanged') }}</span>
            <span v-else class="oa-audit-diff">
              <del :class="{ empty: !change.from }">{{ change.from ? changeValue(change.field, change.from) : at('emptyValue') }}</del>
              <span class="oa-audit-arrow" aria-hidden="true">→</span>
              <ins :class="{ empty: !change.to }">{{ change.to ? changeValue(change.field, change.to) : at('emptyValue') }}</ins>
            </span>
          </li>
        </ul>
      </section>

      <section v-if="opened.request" class="oa-audit-section">
        <h4>{{ at('request') }}</h4>
        <pre class="oa-audit-request">{{ opened.request }}</pre>
        <p class="oa-field-hint">{{ at('requestHint') }}</p>
      </section>

      <div class="oa-audit-links">
        <button type="button" class="oa-btn" @click="showActor(opened)">
          {{ at('byActor', { name: maskUser(opened.actor_username) }) }}
        </button>
        <button v-if="opened.target_id" type="button" class="oa-btn" @click="showTarget(opened)">
          {{ at('onTarget', { name: targetName(opened) }) }}
        </button>
      </div>
    </div>
  </OaPanel>
</template>
