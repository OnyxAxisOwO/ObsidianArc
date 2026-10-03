<script setup lang="ts">
// Usage: what has been spent, by whom, on what — and the instance-wide
// default allowance.
//
// Every figure here is an aggregate over the ledger, so the same page answers
// "what is this costing" and "why did that request fail" without either being
// a separate feature. And every name on it is a way in: pick a model and the
// page becomes that model's — who uses it, when, how often it fails; pick an
// account and it becomes theirs. That is the whole of the drill-down: the
// same questions, asked of a narrower slice of the same ledger.

import { computed, onBeforeUnmount, onMounted, ref, watch } from 'vue';
import { useDocumentVisibility, useIntervalFn } from '@vueuse/core';
import {
  adminApi, emptyPolicy, zoneQuery,
  type Group, type QuotaWindowKind, type UsageBreakdown, type UsagePoint,
  type UsageRecord, type UsageReport, type UsageTotals,
} from '@/admin/api';
import { ApiError } from '@/api/client';
import OaCellStack from '@/components/OaCellStack.vue';
import OaFormSection from '@/components/OaFormSection.vue';
import OaHoldButton from '@/components/OaHoldButton.vue';
import OaNumberField from '@/components/OaNumberField.vue';
import OaPanel from '@/components/OaPanel.vue';
import OaSelect from '@/components/OaSelect.vue';
import OaSelectField from '@/components/OaSelectField.vue';
import OaSwitchField from '@/components/OaSwitchField.vue';
import OaTable from '@/components/OaTable.vue';
import OaTextField from '@/components/OaTextField.vue';
import type { Column, PageState } from '@/components/table-types';
import { celebrate } from '@/composables/useConfetti';
import { t, tn, type StringKey } from '@/composables/useI18n';
import { IconClose } from '@/icons';
import { compactNumber, relativeTime, tokenFigure } from '@/lib/format';
import { maskUser, maskProvider, maskBilling } from '@/admin/safeMode';
import AdminFailure from './AdminFailure.vue';
import CreditsField from './CreditsField.vue';
import StatusBadge from './StatusBadge.vue';
import { useAdminView } from './adminView';
import UsageAllowances from './usage/UsageAllowances.vue';
import UsageBoard from './usage/UsageBoard.vue';
import UsageDelta from './usage/UsageDelta.vue';
import UsageHeatmap from './usage/UsageHeatmap.vue';
import UsageMatrix from './usage/UsageMatrix.vue';
import UsagePlot from './usage/UsagePlot.vue';
import { fillBuckets, formatDuration, percent } from './usage/scale';
import type { BoardMetric, PlotPoint } from './usage/shape';

interface RangePreset {
  key: string;
  /** On the button, where seven of them share one row. */
  label: StringKey;
  /** Above a chart, where there is room to say it in full. */
  long: StringKey;
  hours: number;
}

const RANGE_PRESETS: RangePreset[] = [
  { key: '1h', label: 'range1h', long: 'rangeHour', hours: 1 },
  { key: '24h', label: 'range24h', long: 'rangeDay', hours: 24 },
  { key: '7d', label: 'range7d', long: 'rangeWeek', hours: 24 * 7 },
  { key: '30d', label: 'range30d', long: 'rangeMonth', hours: 24 * 30 },
  { key: '90d', label: 'range90d', long: 'rangeQuarter', hours: 24 * 90 },
  { key: 'all', label: 'rangeAllShort', long: 'rangeAll', hours: 0 },
];

const WINDOWS: QuotaWindowKind[] = ['5h', '1w', '1m'];

type Dimension = 'model' | 'user' | 'group' | 'provider';
type Outcome = '' | 'ok' | 'error' | 'aborted' | 'rejected';
type TrendMetric = 'requests' | 'total_tokens' | 'credits' | 'users';
type CellMetric = 'requests' | 'total_tokens' | 'credits';
type Spread = 'group' | 'provider' | 'status';

const view = useAdminView();
view.setTitle(t('usageTitle'), t('usageSubtitle'));

const currentRPM = ref(0);
const rpmTimer = ref<number | null>(null);

const range = ref<string>(String(selectedRange));
const customRangeOpen = ref(false);
const customRangeError = ref('');
const customStart = ref(savedCustomStart);
const customEnd = ref(savedCustomEnd);
let customSince = savedCustomSince;
let customUntil = savedCustomUntil;

function toLocalISO(date: Date): string {
  const pad = (n: number) => String(n).padStart(2, '0');
  return `${date.getFullYear()}-${pad(date.getMonth() + 1)}-${pad(date.getDate())}T${pad(date.getHours())}:${pad(date.getMinutes())}`;
}

const customLabel = computed(() => range.value === 'custom' && customSince && customUntil
  ? `${new Date(customSince).toLocaleDateString()} – ${new Date(customUntil).toLocaleDateString()}`
  : t('rangeCustomShort'));
const rangeKicker = computed(() => range.value === 'custom'
  ? customLabel.value
  : t((RANGE_PRESETS.find((preset) => preset.key === range.value) ?? RANGE_PRESETS[1]!).long));

// Remembered across visits: an operator who reads tokens by account does it
// again next time, and having to choose twice is friction with no benefit.
const trendMetric = ref<TrendMetric>(selectedTrend);
const modelMetric = ref<BoardMetric>(selectedModelMetric);
const userMetric = ref<BoardMetric>(selectedUserMetric);
const cellMetric = ref<CellMetric>(selectedCellMetric);
const heatMetric = ref<'requests' | 'total_tokens'>(selectedHeatMetric);
const spread = ref<Spread>(selectedSpread);

// Not remembered: a drill-down is a question about one thing, and coming back
// to the page later still narrowed to it reads as missing data.
//
// A link from another section — an account's panel, a model's — arrives
// already narrowed, in the query. It is read once: from then on the filters
// are the page's own. Anything that is not an identifier is dropped here
// rather than sent to be refused.
function linkedFilters(): Record<Dimension, string> & { status: Outcome } {
  const query = new URLSearchParams(window.location.search);
  const id = (key: string): string => {
    const value = query.get(key) ?? '';
    return /^[0-9A-HJKMNP-TV-Z]{26}$/.test(value) ? value : '';
  };
  const status = query.get('status') ?? '';
  return {
    model: id('model'), user: id('user'), group: id('group'), provider: id('provider'),
    status: (['ok', 'error', 'aborted', 'rejected'].includes(status) ? status : '') as Outcome,
  };
}
const filters = ref(linkedFilters());
const anyFilter = computed(() => Object.values(filters.value).some(Boolean));

const report = ref<UsageReport | null>(null);
const records = ref<UsageRecord[]>([]);
const recordTotal = ref(0);
const recordPage = ref<PageState>({ page: 1, pageSize: 20 });
const recordsBusy = ref(false);
const error = ref('');
const loaded = ref(false);
let recordRequest = 0;
// The summary fetch needs a ticket of its own. A request that outlives a
// range or filter change must not paint its stale numbers over the newer
// one's — loadRecords has always done this; this is the other half.
let summaryRequest = 0;
let periodSince = 0;
let periodUntil = 0;
let offsetMs = 0;

/**
 * The names each filter can be set to, kept from the last answer in which
 * that dimension was not itself filtered. Narrowed to one model, the report
 * only knows about that model; the list to pick another from is the one
 * from before.
 */
const known = ref<Record<Dimension, UsageBreakdown[]>>({ model: [], user: [], group: [], provider: [] });

const totals = computed<UsageTotals | null>(() => report.value?.totals ?? null);
const previous = computed(() => report.value?.previous ?? null);
const byModel = computed(() => report.value?.by_model ?? []);
const byUser = computed(() => report.value?.by_user ?? []);
const byGroup = computed(() => report.value?.by_group ?? []);
const byProvider = computed(() => report.value?.by_provider ?? []);
const byStatus = computed(() => report.value?.by_status ?? []);

function figure(value: number | undefined): number {
  return Number.isFinite(value) ? value! : 0;
}

// --- the query --------------------------------------------------------------------

// The period, the filters and the zone the page is currently showing, as a
// query. Shared by load(), the records and the timer below: two copies would
// eventually disagree about which slice is on screen.
function sliceQuery(): URLSearchParams {
  const params = new URLSearchParams();
  if (periodSince > 0) params.set('since', String(periodSince));
  if (periodUntil > 0) params.set('until', String(periodUntil));
  if (filters.value.model) params.set('model_id', filters.value.model);
  if (filters.value.user) params.set('user_id', filters.value.user);
  if (filters.value.group) params.set('group_id', filters.value.group);
  if (filters.value.provider) params.set('provider_id', filters.value.provider);
  if (filters.value.status) params.set('status', filters.value.status);
  return params;
}

function summaryQuery(): string {
  const params = sliceQuery();
  // The server ranks every breakdown by this; the boards re-sort on their
  // own, so what it decides here is only which accounts and models make the
  // grid — the ones that spend the most tokens.
  params.set('metric', 'tokens');
  return `?${params.toString()}&${zoneQuery()}`;
}

function isAbortError(err: unknown): boolean {
  return (
    (err instanceof DOMException && err.name === 'AbortError') ||
    (err instanceof Error && err.name === 'AbortError') ||
    (typeof err === 'object' && err !== null && (err as { name?: string }).name === 'AbortError')
  );
}

let summaryController: AbortController | null = null;
let recordController: AbortController | null = null;

function abortSummary(): void {
  if (summaryController) {
    summaryController.abort();
    summaryController = null;
  }
}

function abortRecords(): void {
  if (recordController) {
    recordController.abort();
    recordController = null;
  }
}

async function loadRecords(quiet = false, signal?: AbortSignal): Promise<void> {
  const ticket = ++recordRequest;
  recordsBusy.value = true;
  try {
    const params = sliceQuery();
    params.set('limit', String(recordPage.value.pageSize));
    params.set('offset', String((recordPage.value.page - 1) * recordPage.value.pageSize));
    const result = await adminApi.usageRecords(`?${params.toString()}`, signal ? { signal } : undefined);
    if (ticket !== recordRequest || signal?.aborted) return;
    records.value = result.records ?? [];
    recordTotal.value = result.total;
  } catch (failure) {
    if (signal?.aborted || isAbortError(failure)) return;
    if (!quiet && ticket === recordRequest) error.value = String(failure);
  } finally {
    if (ticket === recordRequest) recordsBusy.value = false;
    if (recordController?.signal === signal) {
      recordController = null;
    }
  }
}
function changeRecords(next: PageState): void {
  recordPage.value = next;
  abortRecords();
  const ctrl = new AbortController();
  recordController = ctrl;
  void loadRecords(false, ctrl.signal);
}

function applyReport(next: UsageReport): void {
  report.value = next;
  loaded.value = true;
  if (typeof next.current_rpm === 'number') currentRPM.value = next.current_rpm;
  const lists: Record<Dimension, UsageBreakdown[]> = {
    model: next.by_model ?? [], user: next.by_user ?? [], group: next.by_group ?? [], provider: next.by_provider ?? [],
  };
  for (const dimension of Object.keys(lists) as Dimension[]) {
    if (!filters.value[dimension]) known.value[dimension] = lists[dimension];
  }
}

async function load(): Promise<void> {
  abortSummary();
  abortRecords();
  const currentSummaryCtrl = new AbortController();
  const currentRecordCtrl = new AbortController();
  summaryController = currentSummaryCtrl;
  recordController = currentRecordCtrl;

  error.value = '';
  if (range.value === 'custom') {
    periodSince = customSince;
    periodUntil = customUntil;
  } else {
    const preset = RANGE_PRESETS.find((p) => p.key === range.value) ?? RANGE_PRESETS[1]!;
    periodSince = preset.hours > 0 ? Date.now() - preset.hours * 3600_000 : 0;
    periodUntil = 0;
  }
  offsetMs = -new Date().getTimezoneOffset() * 60_000;
  recordPage.value.page = 1;

  const ticket = ++summaryRequest;
  try {
    const [summary] = await Promise.all([
      adminApi.usage(summaryQuery(), { signal: currentSummaryCtrl.signal }),
      loadRecords(false, currentRecordCtrl.signal),
    ]);
    if (ticket !== summaryRequest || currentSummaryCtrl.signal.aborted) return;
    applyReport(summary);
  } catch (failure) {
    if (currentSummaryCtrl.signal.aborted || isAbortError(failure)) return;
    if (ticket === summaryRequest) error.value = failure instanceof Error ? failure.message : String(failure);
  } finally {
    if (summaryController === currentSummaryCtrl) {
      summaryController = null;
    }
    if (recordController === currentRecordCtrl) {
      recordController = null;
    }
    if (!currentSummaryCtrl.signal.aborted) {
      loaded.value = true;
    }
  }
}

// --- range and filters ------------------------------------------------------------------

function onRange(next: string): void {
  if (next === 'custom') {
    openCustomRange();
    return;
  }
  range.value = next;
  selectedRange = next;
  void load();
}

function setFilter(dimension: Dimension | 'status', key: string): void {
  if (filters.value[dimension] === key) return;
  (filters.value as Record<string, string>)[dimension] = key;
  void load();
}

function clearFilters(): void {
  filters.value = { model: '', user: '', group: '', provider: '', status: '' };
  void load();
}

function nameOf(dimension: Dimension, key: string): string {
  const row = known.value[dimension].find((entry) => entry.key === key)
    ?? report.value?.[`by_${dimension}` as const]?.find((entry) => entry.key === key);
  return row?.label || key;
}

function choicesFor(dimension: Dimension, all: StringKey): Array<{ value: string; label: string }> {
  const rows = known.value[dimension].filter((row) => row.key);
  const selected = filters.value[dimension];
  // The one in force stays pickable even if the cached list predates it.
  if (selected && !rows.some((row) => row.key === selected)) rows.unshift({ key: selected, label: nameOf(dimension, selected) } as UsageBreakdown);
  const labelled = (row: UsageBreakdown): string => {
    const name = row.label || row.key;
    if (!row.detail || row.detail === row.label) return name;
    return dimension === 'user' ? `${name} · @${row.detail}` : `${name} · ${row.detail}`;
  };
  return [{ value: '', label: t(all) }, ...rows.slice(0, 200).map((row) => ({ value: row.key, label: labelled(row) }))];
}

const outcomeChoices = computed<Array<{ value: Outcome; label: string }>>(() => [
  { value: '', label: t('filterAllOutcomes') },
  { value: 'ok', label: t('statusOk') },
  { value: 'error', label: t('statusFailed') },
  { value: 'aborted', label: t('statusStopped') },
  { value: 'rejected', label: t('statusRefused') },
]);

/** The one model or account the page has been narrowed to, for the banner. */
const focus = computed(() => {
  if (filters.value.model) {
    const row = byModel.value.find((entry) => entry.key === filters.value.model);
    return { kind: 'model' as const, name: nameOf('model', filters.value.model), row };
  }
  if (filters.value.user) {
    const row = byUser.value.find((entry) => entry.key === filters.value.user);
    return { kind: 'user' as const, name: nameOf('user', filters.value.user), row };
  }
  return null;
});

function focusNotes(): string[] {
  const row = focus.value?.row;
  if (!row) return [t('noRequestsPeriod')];
  const notes: string[] = [];
  if (focus.value?.kind === 'model') {
    if (row.detail) notes.push(row.detail);
    notes.push(tn(row.users, 'boardUsersOne', 'boardUsersOther', { count: row.users }));
  } else {
    if (row.detail) notes.push(`@${row.detail}`);
    notes.push(tn(row.models, 'boardModelsOne', 'boardModelsOther', { count: row.models }));
  }
  notes.push(t('boardRequests', { count: compactNumber(row.requests) }));
  if (row.last_at) notes.push(t('lastUsed', { when: relativeTime(row.last_at) }));
  return notes;
}

// --- the figures ---------------------------------------------------------------------

const successRate = computed(() => {
  const value = totals.value;
  return value && value.requests ? (value.requests - value.errors) / value.requests : null;
});
const meanLatency = computed(() => {
  const value = totals.value;
  return value && value.requests ? figure(value.duration_ms) / value.requests : 0;
});
const previousLatency = computed(() => {
  const value = previous.value;
  return value && value.requests ? figure(value.duration_ms) / value.requests : null;
});
const tokenSplit = computed(() => {
  const value = totals.value;
  const whole = value ? value.input_tokens + value.output_tokens + value.reasoning_tokens : 0;
  if (!value || !whole) return [];
  return [
    { key: 'in', label: t('dashboardInput'), value: value.input_tokens, share: value.input_tokens / whole },
    { key: 'out', label: t('dashboardOutput'), value: value.output_tokens, share: value.output_tokens / whole },
    { key: 'think', label: t('tokensThinking'), value: value.reasoning_tokens, share: value.reasoning_tokens / whole },
  ];
});

// --- the trend ---------------------------------------------------------------------------

const TREND_METRICS: Array<{ value: TrendMetric; label: StringKey }> = [
  { value: 'requests', label: 'statRequests' },
  { value: 'total_tokens', label: 'statTokens' },
  { value: 'credits', label: 'statCredits' },
  { value: 'users', label: 'trendUsers' },
];
const activePoint = ref<number | null>(null);
// Narrowed to one account, "how many accounts" is always one: the options
// that would only ever say so are left out rather than drawn flat.
const trendOptions = computed(() => filters.value.user ? TREND_METRICS.filter((option) => option.value !== 'users') : TREND_METRICS);
const trendShown = computed<TrendMetric>(() => filters.value.user && trendMetric.value === 'users' ? 'requests' : trendMetric.value);

const series = computed<UsagePoint[]>(() => {
  const data = report.value;
  if (!data) return [];
  const zero = (at: number): UsagePoint => ({
    at, requests: 0, input_tokens: 0, output_tokens: 0, reasoning_tokens: 0, total_tokens: 0,
    credits: 0, errors: 0, users: 0, models: 0, duration_ms: 0,
  });
  return fillBuckets(data.series ?? [], data.bucket_ms, {
    since: periodSince, until: periodUntil || Date.now(), offsetMs,
  }, zero);
});
const plotPoints = computed<PlotPoint[]>(() => series.value.map((point) => ({ at: point.at, value: figure(point[trendShown.value]) })));
// Through safe mode when the line is spending: the axis, the readout and the
// peak are all figures somebody recording the screen would otherwise get.
const trendFormat = computed(() => {
  const spending = trendShown.value === 'credits' || trendShown.value.endsWith('tokens');
  const plain = trendShown.value === 'credits'
    ? (value: number) => compactNumber(Math.round(value * 100) / 100)
    : (value: number) => compactNumber(value);
  return spending ? (value: number) => maskBilling(plain(value)) : plain;
});
const trendSummary = computed(() => {
  const point = activePoint.value === null ? null : series.value[activePoint.value];
  if (point) return figure(point[trendShown.value]);
  const value = totals.value;
  // Accounts are counted once for the whole range, not summed per bucket:
  // somebody active every day is one account, not thirty.
  return value ? figure(value[trendShown.value]) : 0;
});
const trendPeak = computed(() => Math.max(0, ...plotPoints.value.map((point) => point.value)));
const plot = ref<InstanceType<typeof UsagePlot> | null>(null);
const trendCaption = computed(() => {
  const point = activePoint.value === null ? null : series.value[activePoint.value];
  if (point && plot.value) return plot.value.describe(point.at);
  return trendShown.value === 'users' ? t('trendDistinct') : t('trendTotal');
});

// --- the boards and the grid -----------------------------------------------------------------

const BOARD_METRICS: Array<{ value: BoardMetric; label: StringKey }> = [
  { value: 'users', label: 'metricPeople' },
  { value: 'requests', label: 'statRequests' },
  { value: 'tokens', label: 'statTokens' },
  { value: 'credits', label: 'statCredits' },
];
const USER_METRICS = BOARD_METRICS.slice(1);
// With one account picked, the model board is that account's models, and
// "how many people" of one person is not a ranking.
const modelOptions = computed(() => filters.value.user ? USER_METRICS : BOARD_METRICS);
const modelShown = computed<BoardMetric>(() => filters.value.user && modelMetric.value === 'users' ? 'tokens' : modelMetric.value);
const CELL_METRICS: Array<{ value: CellMetric; label: StringKey }> = [
  { value: 'requests', label: 'statRequests' },
  { value: 'total_tokens', label: 'statTokens' },
  { value: 'credits', label: 'statCredits' },
];

/**
 * Each shown account's heaviest model, from the grid, for the board's note.
 * Not while one model is picked: every account would be noted as mostly using
 * the model the whole page is already about.
 */
const favourites = computed(() => {
  if (filters.value.model) return {};
  const best = new Map<string, { col: string; tokens: number }>();
  for (const cell of report.value?.matrix?.cells ?? []) {
    const current = best.get(cell.row);
    if (!current || cell.total_tokens > current.tokens) best.set(cell.row, { col: cell.col, tokens: cell.total_tokens });
  }
  const out: Record<string, string> = {};
  for (const [user, entry] of best) out[user] = nameOf('model', entry.col);
  return out;
});
const showMatrix = computed(() => !filters.value.model && !filters.value.user
  && (report.value?.matrix?.rows?.length ?? 0) > 0 && (report.value?.matrix?.cols?.length ?? 0) > 0);

// One setter per control, each remembering its choice for the next visit.
function onTrend(next: TrendMetric): void { trendMetric.value = next; selectedTrend = next; activePoint.value = null; }
function onModelMetric(next: BoardMetric): void { modelMetric.value = next; selectedModelMetric = next; }
function onUserMetric(next: BoardMetric): void { userMetric.value = next; selectedUserMetric = next; }
function onCellMetric(next: CellMetric): void { cellMetric.value = next; selectedCellMetric = next; }
function onHeatMetric(next: 'requests' | 'total_tokens'): void { heatMetric.value = next; selectedHeatMetric = next; }
function onSpreadChoice(next: Spread): void { spread.value = next; selectedSpread = next; }

const statusRows = computed<UsageBreakdown[]>(() => byStatus.value.map((row) => ({
  ...row,
  label: row.key === 'ok' ? t('statusOk') : row.key === 'aborted' ? t('statusStopped')
    : row.key === 'rejected' ? t('statusRefused') : t('statusFailed'),
})));
const spreadRows = computed(() => spread.value === 'group' ? byGroup.value : spread.value === 'provider' ? byProvider.value : statusRows.value);
function onSpread(key: string): void {
  if (spread.value === 'status') setFilter('status', key);
  else setFilter(spread.value, key);
}

// --- the ledger ---------------------------------------------------------------------------

const recordColumns = computed<Array<Column<UsageRecord>>>(() => [
  { key: 'when', header: t('colWhen'), text: (row) => relativeTime(row.started_at), width: '104px' },
  { key: 'user', header: t('colUser'), width: '180px' },
  { key: 'model', header: t('colModel'), width: '200px' },
  { key: 'in', header: t('colIn'), text: (row) => maskBilling(tokenFigure(row.input_tokens, row.estimated)), numeric: true, secondary: true, width: '84px' },
  { key: 'out', header: t('colOut'), text: (row) => maskBilling(tokenFigure(row.output_tokens, row.estimated)), numeric: true, secondary: true, width: '84px' },
  { key: 'credits', header: t('colCredits'), text: (row) => maskBilling(compactNumber(row.credits)), numeric: true, width: '88px' },
  { key: 'took', header: t('colTook'), text: (row) => formatDuration(row.duration_ms), numeric: true, secondary: true, width: '88px' },
  { key: 'status', header: t('colStatus'), width: '150px' },
]);

// --- the default allowance ------------------------------------------------------------------

const policyOpen = ref(false);
const policyBusy = ref(false);
const policyError = ref('');
const policyForm = ref({
  rpm: null as number | null,
  tpm: null as number | null,
  windows: {} as Record<QuotaWindowKind, { enabled: boolean; requests: number | null; tokens: number | null; credits: number | null }>,
});

async function openPolicy(): Promise<void> {
  let policy = emptyPolicy('global', '');
  try {
    const { policies } = await adminApi.policies();
    policy = policies.find((entry) => entry.scope === 'global') ?? policy;
  } catch {
    // Fall through with the blank policy; saving will create it.
  }
  const windows = {} as typeof policyForm.value.windows;
  for (const kind of WINDOWS) {
    const limits = policy.windows[kind];
    windows[kind] = {
      enabled: limits?.enabled === true,
      requests: limits?.requests ?? null,
      tokens: limits?.tokens ?? null,
      credits: limits?.credits ?? null,
    };
  }
  policyForm.value = { rpm: policy.rpm, tpm: policy.tpm, windows };
  policyError.value = '';
  policyOpen.value = true;
}

async function savePolicy(): Promise<void> {
  policyBusy.value = true;
  try {
    await adminApi.savePolicy({
      scope: 'global',
      scope_id: '',
      rpm: policyForm.value.rpm,
      tpm: policyForm.value.tpm,
      windows: Object.fromEntries(WINDOWS.map((kind) => [kind, {
        enabled: policyForm.value.windows[kind].enabled ? true : null,
        requests: policyForm.value.windows[kind].requests,
        tokens: policyForm.value.windows[kind].tokens,
        credits: policyForm.value.windows[kind].credits,
      }])),
    });
    policyOpen.value = false;
    view.reload();
  } catch (failure) {
    policyError.value = failure instanceof ApiError ? failure.message : String(failure);
  } finally {
    policyBusy.value = false;
  }
}

// --- putting an allowance back --------------------------------------------------------------

const resetOpen = ref(false);
const resetScope = ref<'all' | 'group' | 'user'>('all');
const resetGroup = ref('');
const resetAccount = ref('');
const resetGroups = ref<Pick<Group, 'id' | 'name'>[]>([]);
const resetTitle = ref('');
const resetError = ref('');

async function openReset(): Promise<void> {
  resetScope.value = 'all';
  resetAccount.value = '';
  resetError.value = '';
  resetTitle.value = t('resetQuota');
  try {
    ({ groups: resetGroups.value } = await adminApi.groupOptions());
    resetGroup.value = resetGroups.value[0]?.id ?? '';
  } catch {
    // The group option simply will not be offered. Everyone and one account
    // still work, and refusing to open at all would be worse.
  }
  resetOpen.value = true;
}

async function runReset(): Promise<void> {
  const scope = resetScope.value;
  if (scope === 'group' && !resetGroup.value) {
    resetError.value = t('resetNoGroup');
    return;
  }
  if (scope === 'user' && !resetAccount.value.trim()) {
    resetError.value = t('resetNoAccount');
    return;
  }
  try {
    const body = scope === 'all'
      ? { scope } as const
      : { scope, id: scope === 'group' ? resetGroup.value : resetAccount.value.trim() };
    const { accounts } = await adminApi.resetQuota(body);
    resetTitle.value = t('resetDone', { count: accounts });
    resetError.value = '';
    celebrate();
    view.reload();
  } catch (failure) {
    resetError.value = failure instanceof ApiError ? failure.message : String(failure);
  }
}

// --- a custom range ------------------------------------------------------------------------

function openCustomRange(): void {
  if (!customStart.value) customStart.value = toLocalISO(new Date(Date.now() - 24 * 3600_000));
  if (!customEnd.value) customEnd.value = toLocalISO(new Date());
  customRangeError.value = '';
  customRangeOpen.value = true;
}

function closeCustomRange(): void {
  customRangeOpen.value = false;
  customRangeError.value = '';
  if (selectedRange !== 'custom') range.value = selectedRange;
}

function applyCustomRange(): void {
  if (!customStart.value) {
    customRangeError.value = t('customStartRequired');
    return;
  }
  if (!customEnd.value) {
    customRangeError.value = t('customEndRequired');
    return;
  }
  const startMs = new Date(customStart.value).getTime();
  const endMs = new Date(customEnd.value).getTime();
  if (isNaN(startMs) || isNaN(endMs) || startMs >= endMs) {
    customRangeError.value = t('customInvalidRange');
    return;
  }
  customSince = startMs;
  customUntil = endMs;
  savedCustomSince = startMs;
  savedCustomUntil = endMs;
  savedCustomStart = customStart.value;
  savedCustomEnd = customEnd.value;
  range.value = 'custom';
  selectedRange = 'custom';
  customRangeError.value = '';
  customRangeOpen.value = false;
  void load();
}

type Preset = 'today' | 'yesterday' | 'thisWeek' | 'thisMonth' | 'last7d' | 'last30d';
const CUSTOM_PRESETS: Array<{ key: Preset; label: StringKey }> = [
  { key: 'today', label: 'presetToday' },
  { key: 'yesterday', label: 'presetYesterday' },
  { key: 'thisWeek', label: 'presetThisWeek' },
  { key: 'thisMonth', label: 'presetThisMonth' },
  { key: 'last7d', label: 'presetLast7Days' },
  { key: 'last30d', label: 'presetLast30Days' },
];

function setPreset(preset: Preset): void {
  const now = new Date();
  const start = new Date(now);
  const end = new Date(now);

  switch (preset) {
    case 'today':
      start.setHours(0, 0, 0, 0);
      break;
    case 'yesterday':
      start.setDate(start.getDate() - 1);
      start.setHours(0, 0, 0, 0);
      end.setDate(end.getDate() - 1);
      end.setHours(23, 59, 59, 999);
      break;
    case 'thisWeek': {
      const day = start.getDay();
      const diff = day === 0 ? 6 : day - 1;
      start.setDate(start.getDate() - diff);
      start.setHours(0, 0, 0, 0);
      break;
    }
    case 'thisMonth':
      start.setDate(1);
      start.setHours(0, 0, 0, 0);
      break;
    case 'last7d':
      start.setTime(now.getTime() - 7 * 24 * 3600_000);
      break;
    case 'last30d':
      start.setTime(now.getTime() - 30 * 24 * 3600_000);
      break;
  }
  customStart.value = toLocalISO(start);
  customEnd.value = toLocalISO(end);
  customRangeError.value = '';
}

// --- keeping it current --------------------------------------------------------------------

async function refreshRPM(): Promise<void> {
  if (visibility.value === 'hidden') return;
  try {
    const res = await adminApi.rpm();
    currentRPM.value = res.rpm;
  } catch {
    // Keep existing RPM on transient error
  }
}

// The page re-reads itself while it is open: these are aggregates over the
// ledger and they move the moment a request lands, so an operator watching a
// running instance should not have to reach for a refresh.
//
// It reuses the slice load() worked out and leaves recordPage alone — the
// timer only asks the same question again; it does not change the operator's
// filters or throw them back to the first page. A failed refresh is ignored,
// because the last good figures are a better answer than an error where they
// were, and success clears the error so a page that opened against an
// unreachable server recovers on its own.
const REFRESH_MS = 15000;

// Nobody reads a background tab. Each refresh recomputes every aggregate on
// the page over the whole range — a dozen queries across the ledger — and a
// tab left open overnight used to run them four times a minute for no one.
// It catches up the moment it is looked at again.
const visibility = useDocumentVisibility();
watch(visibility, (now, was) => {
  if (now === 'visible' && was === 'hidden') { refreshQuietly(); void refreshRPM(); }
});

function refreshQuietly(): void {
  if (visibility.value === 'hidden' || !loaded.value) return;
  abortSummary();
  abortRecords();
  const currentSummaryCtrl = new AbortController();
  const currentRecordCtrl = new AbortController();
  summaryController = currentSummaryCtrl;
  recordController = currentRecordCtrl;

  const ticket = ++summaryRequest;
  void adminApi.usage(summaryQuery(), { signal: currentSummaryCtrl.signal }).then((summary) => {
    if (ticket !== summaryRequest || currentSummaryCtrl.signal.aborted) return;
    applyReport(summary);
    error.value = '';
  }).catch((err) => {
    if (isAbortError(err)) return;
  }).finally(() => {
    if (summaryController === currentSummaryCtrl) {
      summaryController = null;
    }
  });
  void loadRecords(true, currentRecordCtrl.signal);
}

useIntervalFn(refreshQuietly, REFRESH_MS);

onMounted(() => {
  void load();
  const timer = window.setInterval(refreshRPM, 10000);
  rpmTimer.value = timer;
});

onBeforeUnmount(() => {
  if (rpmTimer.value !== null) clearInterval(rpmTimer.value);
  abortSummary();
  abortRecords();
});
</script>

<script lang="ts">
let selectedRange = '24h';
let selectedTrend: 'requests' | 'total_tokens' | 'credits' | 'users' = 'requests';
let selectedModelMetric: 'users' | 'requests' | 'tokens' | 'credits' = 'users';
let selectedUserMetric: 'users' | 'requests' | 'tokens' | 'credits' = 'tokens';
let selectedCellMetric: 'requests' | 'total_tokens' | 'credits' = 'total_tokens';
let selectedHeatMetric: 'requests' | 'total_tokens' = 'requests';
let selectedSpread: 'group' | 'provider' | 'status' = 'group';
let savedCustomStart = '';
let savedCustomEnd = '';
let savedCustomSince = 0;
let savedCustomUntil = 0;
</script>

<template>
  <Teleport :to="view.actionsHost">
    <div class="oa-segment oa-range" role="group" :aria-label="t('rangeLabel')">
      <button
        v-for="preset in RANGE_PRESETS" :key="preset.key" type="button"
        :aria-pressed="range === preset.key" @click="onRange(preset.key)"
      >{{ t(preset.label) }}</button>
      <button type="button" :aria-pressed="range === 'custom'" @click="onRange('custom')">{{ customLabel }}</button>
    </div>
    <button id="defaultLimits" type="button" class="oa-btn" @click="openPolicy">{{ t('defaultLimits') }}</button>
    <button id="resetQuota" type="button" class="oa-btn oa-btn-danger" @click="openReset">{{ t('resetQuota') }}</button>
  </Teleport>

  <AdminFailure v-if="error" :message="error" @retry="load" />
  <div v-else-if="!loaded" class="oa-report-loading" role="status" :aria-label="t('loading')"><div /><div /><div /></div>

  <div v-else class="oa-report">
    <div id="usageFilters" class="oa-report-filters">
      <OaSelect class="oa-filter-select" :class="{ set: !!filters.model }" :model-value="filters.model" :aria-label="t('colModel')"
        :choices="choicesFor('model', 'filterAllModels')" @update:model-value="setFilter('model', $event)" />
      <OaSelect class="oa-filter-select" :class="{ set: !!filters.user }" :model-value="filters.user" :aria-label="t('colUser')"
        :choices="choicesFor('user', 'filterAllUsers')" @update:model-value="setFilter('user', $event)" />
      <OaSelect class="oa-filter-select" :class="{ set: !!filters.group }" :model-value="filters.group" :aria-label="t('colGroup')"
        :choices="choicesFor('group', 'anyGroup')" @update:model-value="setFilter('group', $event)" />
      <OaSelect class="oa-filter-select" :class="{ set: !!filters.provider }" :model-value="filters.provider" :aria-label="t('colProvider')"
        :choices="choicesFor('provider', 'filterAllProviders')" @update:model-value="setFilter('provider', $event)" />
      <OaSelect class="oa-filter-select" :class="{ set: !!filters.status }" :model-value="filters.status" :aria-label="t('colStatus')"
        :choices="outcomeChoices" :searchable="false" @update:model-value="setFilter('status', $event)" />
      <button v-if="anyFilter" type="button" class="oa-text-btn" @click="clearFilters">{{ t('filterClear') }}</button>
      <span class="oa-report-live" :title="t('rpmRealtime')"><span class="oa-live-dot" aria-hidden="true" />{{ t('liveRPM', { count: compactNumber(currentRPM) }) }}</span>
    </div>

    <section v-if="focus" class="oa-report-focus" :aria-label="focus.name">
      <span class="oa-report-focus-mark" :class="focus.kind" aria-hidden="true">{{ Array.from(focus.name)[0]?.toLocaleUpperCase() }}</span>
      <div class="oa-report-focus-copy">
        <span class="oa-kicker">{{ focus.kind === 'model' ? t('colModel') : t('colAccount') }}</span>
        <h2>{{ focus.name }}</h2>
        <p>{{ focusNotes().join(' · ') }}</p>
      </div>
      <button type="button" class="oa-btn" @click="setFilter(focus.kind, '')">
        <IconClose :size="14" aria-hidden="true" />{{ t('focusBack') }}
      </button>
    </section>

    <section id="secTotals" class="oa-kpis" :aria-label="t('secTotals')">
      <article class="oa-kpi featured">
        <span class="oa-kpi-label">{{ t('statRequests') }}</span>
        <strong class="oa-kpi-value" :title="figure(totals?.requests).toLocaleString()">{{ compactNumber(figure(totals?.requests)) }}</strong>
        <span class="oa-kpi-foot">
          <UsageDelta :current="figure(totals?.requests)" :previous="previous?.requests" />
          <span>{{ successRate === null ? t('noRequestsPeriod') : t('kpiSuccess', { rate: percent(successRate) }) }}</span>
        </span>
      </article>
      <article class="oa-kpi">
        <span class="oa-kpi-label">{{ t('statTokens') }}</span>
        <strong class="oa-kpi-value" :title="maskBilling(figure(totals?.total_tokens).toLocaleString())">{{ maskBilling(compactNumber(figure(totals?.total_tokens))) }}</strong>
        <span v-if="tokenSplit.length" class="oa-kpi-split" aria-hidden="true">
          <span v-for="part in tokenSplit" :key="part.key" :class="part.key" :style="{ flexGrow: part.share }" />
        </span>
        <span class="oa-kpi-foot">
          <UsageDelta :current="figure(totals?.total_tokens)" :previous="previous?.total_tokens" />
          <span v-if="tokenSplit.length">{{ tokenSplit.map((part) => `${part.label} ${maskBilling(compactNumber(part.value))}`).join(' · ') }}</span>
        </span>
      </article>
      <article class="oa-kpi">
        <span class="oa-kpi-label">{{ t('statCredits') }}</span>
        <strong class="oa-kpi-value" :title="maskBilling(figure(totals?.credits).toLocaleString())">{{ maskBilling(compactNumber(Math.round(figure(totals?.credits) * 100) / 100)) }}</strong>
        <span class="oa-kpi-foot">
          <UsageDelta :current="figure(totals?.credits)" :previous="previous?.credits" />
          <span v-if="totals?.requests">{{ t('kpiPerRequest', { value: maskBilling(compactNumber(Math.round(figure(totals?.credits) / totals.requests * 100) / 100)) }) }}</span>
        </span>
      </article>
      <!-- One account's page counts its models instead: "active accounts: 1"
           is true and says nothing. -->
      <article v-if="filters.user" class="oa-kpi">
        <span class="oa-kpi-label">{{ t('kpiModels') }}</span>
        <strong class="oa-kpi-value">{{ compactNumber(figure(totals?.models)) }}</strong>
        <span class="oa-kpi-foot">
          <UsageDelta :current="figure(totals?.models)" :previous="previous?.models" />
          <span v-if="favourites[filters.user]">{{ t('boardFavourite', { model: favourites[filters.user]! }) }}</span>
        </span>
      </article>
      <article v-else class="oa-kpi">
        <span class="oa-kpi-label">{{ t('kpiActiveUsers') }}</span>
        <strong class="oa-kpi-value">{{ compactNumber(figure(totals?.users)) }}</strong>
        <span class="oa-kpi-foot">
          <UsageDelta :current="figure(totals?.users)" :previous="previous?.users" />
          <span v-if="totals?.models">{{ t('kpiModelsUsed', { count: totals.models }) }}</span>
        </span>
      </article>
      <article class="oa-kpi">
        <span class="oa-kpi-label">{{ t('kpiLatency') }}</span>
        <strong class="oa-kpi-value">{{ formatDuration(meanLatency) }}</strong>
        <span class="oa-kpi-foot">
          <UsageDelta :current="meanLatency" :previous="previousLatency" inverse />
          <span>{{ t('kpiLatencyNote') }}</span>
        </span>
      </article>
      <article class="oa-kpi" :class="{ alarming: figure(totals?.errors) > 0 }">
        <span class="oa-kpi-label">{{ t('dashboardFailedRequests') }}</span>
        <strong class="oa-kpi-value">{{ compactNumber(figure(totals?.errors)) }}</strong>
        <span class="oa-kpi-foot">
          <UsageDelta :current="figure(totals?.errors)" :previous="previous?.errors" inverse />
          <span v-if="totals?.requests">{{ t('dashboardFailureRate', { rate: percent(figure(totals.errors) / totals.requests) }) }}</span>
        </span>
      </article>
    </section>

    <section id="secOverTime" class="oa-viz-card">
      <header class="oa-viz-head">
        <div><span class="oa-kicker">{{ rangeKicker }}</span><h2>{{ t('secOverTime') }}</h2></div>
        <div class="oa-segment" role="group" :aria-label="t('dashboardTrendMetric')">
          <button v-for="option in trendOptions" :key="option.value" type="button" :aria-pressed="trendShown === option.value"
            @click="onTrend(option.value)">{{ t(option.label) }}</button>
        </div>
      </header>
      <div class="oa-viz-summary">
        <strong>{{ trendFormat(trendSummary) }}</strong>
        <span>{{ trendCaption }}</span>
      </div>
      <UsagePlot
        v-if="plotPoints.some((point) => point.value > 0)"
        ref="plot" v-model:active="activePoint" :points="plotPoints" :bucket-ms="report?.bucket_ms ?? 3600000"
        :format="trendFormat" :label="t('dashboardChartKeyboard')" :height="190"
      />
      <p v-else class="oa-viz-empty">{{ t('noRequestsPeriod') }}</p>
      <footer class="oa-viz-foot">
        <span><span class="oa-dashboard-dot" />{{ (report?.bucket_ms ?? 0) >= 86400000 ? t('trendDaily') : t('trendHourly') }}</span>
        <span>{{ t('dashboardPeak', { value: trendFormat(trendPeak) }) }}</span>
      </footer>
    </section>

    <UsageAllowances v-if="!filters.user" :group-id="filters.group" @select="setFilter('user', $event)" />

    <div class="oa-report-row">
      <section v-if="!filters.model" id="secPopularModels" class="oa-viz-card">
        <header class="oa-viz-head">
          <div><span class="oa-kicker">{{ t(filters.user ? 'boardTheirModelsHint' : 'boardPopularHint') }}</span><h2>{{ t(filters.user ? 'boardTheirModels' : 'boardPopularModels') }}</h2></div>
          <div class="oa-segment" role="group" :aria-label="t('rankMetric')">
            <button v-for="option in modelOptions" :key="option.value" type="button" :aria-pressed="modelShown === option.value"
              @click="onModelMetric(option.value)">{{ t(option.label) }}</button>
          </div>
        </header>
        <UsageBoard :rows="byModel" kind="model" :metric="modelShown" :reach="!filters.user" :empty-text="t('nothingInPeriod')" @select="setFilter('model', $event)" />
      </section>
      <section v-if="!filters.user" id="secTopUsers" class="oa-viz-card">
        <header class="oa-viz-head">
          <div><span class="oa-kicker">{{ t('boardTopUsersHint') }}</span><h2>{{ t('secTopUsers') }}</h2></div>
          <div class="oa-segment" role="group" :aria-label="t('rankMetric')">
            <button v-for="option in USER_METRICS" :key="option.value" type="button" :aria-pressed="userMetric === option.value"
              @click="onUserMetric(option.value)">{{ t(option.label) }}</button>
          </div>
        </header>
        <UsageBoard :rows="byUser" kind="user" :metric="userMetric" :reach="!filters.model" :favourites="favourites" :empty-text="t('nothingInPeriod')" @select="setFilter('user', $event)" />
      </section>
    </div>

    <section v-if="showMatrix && report" id="secWhoUsesWhat" class="oa-viz-card">
      <header class="oa-viz-head">
        <div><span class="oa-kicker">{{ t('matrixHint') }}</span><h2>{{ t('matrixTitle') }}</h2></div>
        <div class="oa-segment" role="group" :aria-label="t('rankMetric')">
          <button v-for="option in CELL_METRICS" :key="option.value" type="button" :aria-pressed="cellMetric === option.value"
            @click="onCellMetric(option.value)">{{ t(option.label) }}</button>
        </div>
      </header>
      <UsageMatrix
        :matrix="report.matrix" :users="byUser" :models="byModel" :metric="cellMetric"
        :format="(value) => compactNumber(cellMetric === 'credits' ? Math.round(value * 100) / 100 : value)"
        @user="setFilter('user', $event)" @model="setFilter('model', $event)"
      />
    </section>

    <div class="oa-report-row oa-report-row-lead">
      <section id="secWhen" class="oa-viz-card">
        <header class="oa-viz-head">
          <div><span class="oa-kicker">{{ t('heatmapHint') }}</span><h2>{{ t('heatmapTitle') }}</h2></div>
          <div class="oa-segment" role="group" :aria-label="t('rankMetric')">
            <button type="button" :aria-pressed="heatMetric === 'requests'" @click="onHeatMetric('requests')">{{ t('statRequests') }}</button>
            <button type="button" :aria-pressed="heatMetric === 'total_tokens'" @click="onHeatMetric('total_tokens')">{{ t('statTokens') }}</button>
          </div>
        </header>
        <UsageHeatmap :slots="report?.heatmap ?? []" :metric="heatMetric" :format="(value) => heatMetric === 'requests' ? t('boardRequests', { count: compactNumber(value) }) : t('boardTokens', { count: maskBilling(compactNumber(value)) })" />
      </section>
      <section id="secSpread" class="oa-viz-card">
        <header class="oa-viz-head">
          <div><span class="oa-kicker">{{ t('spreadHint') }}</span><h2>{{ t('spreadTitle') }}</h2></div>
          <div class="oa-segment" role="group" :aria-label="t('spreadTitle')">
            <button type="button" :aria-pressed="spread === 'group'" @click="onSpreadChoice('group')">{{ t('colGroup') }}</button>
            <button type="button" :aria-pressed="spread === 'provider'" @click="onSpreadChoice('provider')">{{ t('colProvider') }}</button>
            <button type="button" :aria-pressed="spread === 'status'" @click="onSpreadChoice('status')">{{ t('spreadOutcomes') }}</button>
          </div>
        </header>
        <UsageBoard :rows="spreadRows" :kind="spread === 'status' ? 'provider' : spread" metric="requests" :limit="5" :empty-text="t('nothingInPeriod')" @select="onSpread" />
      </section>
    </div>

    <section id="secRecords" class="oa-viz-card">
      <header class="oa-viz-head">
        <div><span class="oa-kicker">{{ t('recordsHint') }}</span><h2>{{ t('secRequestsN', { count: recordTotal }) }}</h2></div>
      </header>
      <OaTable
        :pagination="{ ...recordPage, total: recordTotal }"
        :busy="recordsBusy"
        :columns="recordColumns"
        :rows="records"
        :empty="t('noRequestsPeriod')"
        :muted="(row) => row.status !== 'ok'"
        @page="changeRecords"
      >
        <template #cell-user="{ row }">
          <OaCellStack
            :title="maskUser(row.nickname || row.username || row.user_id)"
            :sub="row.nickname && row.username ? `@${maskUser(row.username)}` : ''"
          />
        </template>
        <template #cell-model="{ row }">
          <OaCellStack :title="row.model_name || '—'" :sub="maskProvider(row.provider_name)" />
        </template>
        <template #cell-status="{ row }">
          <StatusBadge :status="row.status" :error-code="row.error_code" />
        </template>
      </OaTable>
    </section>
  </div>

  <!-- The instance default: what applies to anyone whose group and account say
       nothing. Edited here rather than on the groups page because it is not a
       group. -->
  <OaPanel
    v-if="policyOpen"
    :title="t('defaultLimits')"
    :confirm-label="t('save')"
    :busy="policyBusy"
    :error="policyError"
    @close="policyOpen = false"
    @confirm="savePolicy"
  >
    <p class="oa-field-hint">{{ t('adminsExemptHint') }}</p>
    <OaNumberField
      v-model="policyForm.rpm"
      :label="t('requestsPerMinute')"
      :placeholder="t('noLimit')"
      :min="0"
      :hint="t('defaultLimitsHint')"
    />
    <OaNumberField
      v-model="policyForm.tpm"
      :label="t('tokensPerMinute')"
      :placeholder="t('noLimit')"
      :min="0"
    />
    <template v-for="kind in WINDOWS" :key="kind">
      <OaFormSection :title="t('everyWindow', { window: kind })" />
      <OaSwitchField
        v-model="policyForm.windows[kind].enabled"
        :label="t('enforceTheWindow', { window: kind })"
      />
      <OaNumberField
        v-model="policyForm.windows[kind].requests"
        :label="t('limitRequests')"
        :placeholder="t('noLimit')"
        :min="0"
      />
      <OaNumberField
        v-model="policyForm.windows[kind].tokens"
        :label="t('limitTokens')"
        :placeholder="t('noLimit')"
        :min="0"
      />
      <CreditsField v-model="policyForm.windows[kind].credits" />
    </template>
  </OaPanel>

  <!-- The scope is chosen before the button that does it appears, because
       "reset" with no number beside it is the same word whether it means one
       person or everyone, and the difference is the entire decision. -->
  <OaPanel
    v-if="resetOpen"
    :title="resetTitle"
    :footer="false"
    :error="resetError"
    @close="resetOpen = false"
  >
    <p class="oa-field-hint">{{ t('resetExplain') }}</p>
    <OaSelectField
      v-model="resetScope"
      :label="t('resetScope')"
      :options="[
        { value: 'all', label: t('resetScopeAll') },
        { value: 'group', label: t('resetScopeGroup') },
        { value: 'user', label: t('resetScopeUser') },
      ]"
    />
    <OaSelectField
      v-if="resetScope === 'group'"
      v-model="resetGroup"
      :label="t('groupsTitle')"
      :options="resetGroups.map((entry) => ({ value: entry.id, label: entry.name }))"
    />
    <OaTextField
      v-if="resetScope === 'user'"
      v-model="resetAccount"
      :label="t('resetAccountID')"
      placeholder="01ARZ3NDEKTSV4RRFFQ69G5FAV"
      :hint="t('resetAccountIDHint')"
      monospace
    />
    <OaHoldButton
      :label="t('resetConfirmLabel')"
      :holding-label="t('resetHolding')"
      @fire="runReset"
    />
    <p class="oa-field-hint oa-hold-note">{{ t('resetHoldHint') }}</p>
  </OaPanel>

  <OaPanel
    v-if="customRangeOpen"
    :title="t('customTimeTitle')"
    :confirm-label="t('apply')"
    :error="customRangeError"
    @close="closeCustomRange"
    @confirm="applyCustomRange"
  >
    <p class="oa-field-hint">{{ t('customTimeHint') }}</p>
    <div class="oa-chip-row">
      <button v-for="preset in CUSTOM_PRESETS" :key="preset.key" type="button" class="oa-chip-btn" @click="setPreset(preset.key)">{{ t(preset.label) }}</button>
    </div>
    <OaTextField
      v-model="customStart"
      type="datetime-local"
      :label="t('customStart')"
    />
    <OaTextField
      v-model="customEnd"
      type="datetime-local"
      :label="t('customEnd')"
    />
  </OaPanel>
</template>
