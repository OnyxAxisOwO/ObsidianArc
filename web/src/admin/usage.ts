// Usage: what has been spent, by whom, on what — and the instance-wide
// default allowance.
//
// Every figure here is an aggregate over the ledger, so the same page answers
// "what is this costing" and "why did that request fail" without either being
// a separate feature.

import { ApiError } from '../api/client';
import { t, type StringKey } from '../i18n';
import { button, clear, el } from '../ui/dom';
import { openPanel } from '../ui/panel';
import { numberField, section, selectField, switchField, textField } from '../ui/form';
import { compactNumber, relativeTime, renderTable, stacked } from '../ui/table';
import { renderChart, type ChartShape } from '../ui/chart';
import {
  adminApi,
  emptyPolicy,
  type Group,
  type QuotaWindowKind,
  type UsageBreakdown,
  type UsageMetric,
  type UsagePoint,
} from './api';
import { section as panel, statGrid, statusBadge } from './dashboard';
import { creditsField, failure, type AdminView } from './admin-page';

const RANGES: Array<{ label: StringKey; hours: number }> = [
  { label: 'rangeDay', hours: 24 },
  { label: 'rangeWeek', hours: 24 * 7 },
  { label: 'rangeMonth', hours: 24 * 30 },
];

let selectedRange = 1;

// Remembered across visits: an operator who looks at credits by user does it
// again next time, and having to choose twice is friction with no benefit.
let selectedMetric: UsageMetric = 'credits';
let selectedShape: ChartShape = 'bar';
let selectedDimension: 'model' | 'user' | 'provider' = 'model';

/**
 * Putting an allowance back to full, deliberately.
 *
 * The scope is chosen before the button that does it appears, and the button
 * says how many accounts it is about to touch — because "reset" with no
 * number beside it is the same word whether it means one person or everyone,
 * and the difference is the entire decision.
 */
async function openReset(view: AdminView): Promise<void> {
  let groups: Group[] = [];
  try {
    ({ groups } = await adminApi.groups());
  } catch {
    // The group option simply will not be offered. Everyone and one account
    // still work, and refusing to open at all would be worse.
  }

  const scope = selectField<'all' | 'group' | 'user'>({
    label: t('resetScope'),
    value: 'all',
    options: [
      { value: 'all', label: t('resetScopeAll') },
      { value: 'group', label: t('resetScopeGroup') },
      { value: 'user', label: t('resetScopeUser') },
    ],
    onChange: () => panel.rebuild(),
  });

  const group = selectField({
    label: t('groupsTitle'),
    value: groups[0]?.id ?? '',
    options: groups.map((entry) => ({ value: entry.id, label: entry.name })),
  });

  const account = textField({
    label: t('resetAccountID'),
    placeholder: '01ARZ3NDEKTSV4RRFFQ69G5FAV',
    hint: t('resetAccountIDHint'),
    monospace: true,
  });

  const panel = openPanel({
    host: view.host,
    title: t('resetQuota'),
    cancelLabel: t('close'),
    destructive: {
      label: t('resetConfirmLabel'),
      // The second click is the act. The first only arms it, which is the
      // last moment anyone reads the scope they picked.
      confirm: t('resetConfirm'),
      onSelect: async (handle) => {
        const chosen = scope.value();
        if (chosen === 'group' && !group.value()) {
          handle.setError(t('resetNoGroup'));
          return;
        }
        if (chosen === 'user' && !account.value()) {
          handle.setError(t('resetNoAccount'));
          return;
        }

        handle.setBusy(true);
        try {
          const body = chosen === 'all'
            ? { scope: chosen } as const
            : { scope: chosen, id: chosen === 'group' ? group.value() : account.value() };
          const { accounts } = await adminApi.resetQuota(body);
          handle.setBusy(false);
          handle.setTitle(t('resetDone', { count: accounts }));
          handle.setError('');
          view.reload();
        } catch (error) {
          handle.setBusy(false);
          handle.setError(error instanceof ApiError ? error.message : String(error));
        }
      },
    },
    build: (body) => {
      body.appendChild(el('p', 'oa-field-hint', t('resetExplain')));
      body.appendChild(scope.element);
      if (scope.value() === 'group') body.appendChild(group.element);
      if (scope.value() === 'user') body.appendChild(account.element);
    },
  });
}

export async function renderUsage(view: AdminView): Promise<void> {
  view.setTitle(t('usageTitle'));

  const since = Date.now() - RANGES[selectedRange]!.hours * 3600_000;
  // The ranking is done in SQL, so which metric is being asked for has to go
  // with the request: the top fifty by credits is not the top fifty by
  // request count.
  const query = `?since=${since}&metric=${selectedMetric}`;

  let summary;
  let records;
  try {
    [summary, records] = await Promise.all([
      adminApi.usage(query),
      adminApi.usageRecords(`${query}&limit=50`),
    ]);
  } catch (error) {
    failure(view, error);
    return;
  }

  clear(view.actions);

  const range = el('select');
  RANGES.forEach((entry, index) => {
    const option = el('option', null, t(entry.label));
    option.value = String(index);
    range.appendChild(option);
  });
  range.value = String(selectedRange);
  range.addEventListener('change', () => {
    selectedRange = Number(range.value);
    view.reload();
  });
  const wrap = el('div', 'oa-filters');
  wrap.style.margin = '0';
  wrap.appendChild(range);
  view.actions.appendChild(wrap);
  view.actions.appendChild(button('oa-btn', t('defaultLimits'), () => void editGlobalPolicy(view)));
  view.actions.appendChild(button('oa-btn oa-btn-danger', t('resetQuota'), () => void openReset(view)));

  clear(view.body);

  const totals = summary.totals;
  view.body.appendChild(panel(t('secTotals'), statGrid([
    { label: t('statRequests'), value: compactNumber(totals.requests), note: totals.errors ? t('nFailed', { count: totals.errors }) : t('allFine') },
    { label: t('statInputTokens'), value: compactNumber(totals.input_tokens) },
    { label: t('statOutputTokens'), value: compactNumber(totals.output_tokens) },
    { label: t('statReasoningTokens'), value: compactNumber(totals.reasoning_tokens) },
    { label: t('statCredits'), value: compactNumber(totals.credits) },
  ])));

  view.body.appendChild(panel(t('secOverTime'), chart(summary.series, summary.bucket_ms)));

  view.body.appendChild(panel(t('secRanking'), ranking({
    model: summary.by_model,
    user: summary.by_user,
    provider: summary.by_provider,
  })));

  view.body.appendChild(panel(t('secByModel'), renderTable({
    columns: [
      { header: t('colModel'), cell: (row) => row.label || row.key || '—' },
      { header: t('colRequests'), cell: (row) => compactNumber(row.requests), numeric: true },
      { header: t('colTokens'), cell: (row) => compactNumber(row.total_tokens), numeric: true },
      { header: t('colCredits'), cell: (row) => compactNumber(row.credits), numeric: true },
      { header: t('colFailed'), cell: (row) => compactNumber(row.errors), numeric: true, secondary: true },
    ],
    rows: summary.by_model,
    empty: t('nothingInPeriod'),
  })));

  view.body.appendChild(panel(t('secByProvider'), renderTable({
    columns: [
      { header: t('colProvider'), cell: (row) => row.label || row.key || '—' },
      { header: t('colRequests'), cell: (row) => compactNumber(row.requests), numeric: true },
      { header: t('colTokens'), cell: (row) => compactNumber(row.total_tokens), numeric: true },
      { header: t('colCredits'), cell: (row) => compactNumber(row.credits), numeric: true },
    ],
    rows: summary.by_provider,
    empty: t('nothingInPeriod'),
  })));

  /**
   * The same numbers as the tables below, as a shape.
   *
   * Three choices rather than one, because "who used the most" has three
   * defensible answers and a chart that picks silently is a chart that
   * misleads. Shape and dimension repaint from what is already loaded;
   * changing the metric refetches, because the ranking is the server's.
   */
  function ranking(sets: Record<'model' | 'user' | 'provider', UsageBreakdown[]>): HTMLElement {
    const wrap = el('div', 'oa-ranking');
    const canvas = el('div', 'oa-ranking-canvas');

    const dimension = selectField<'model' | 'user' | 'provider'>({
      label: t('rankBy'),
      value: selectedDimension,
      options: [
        { value: 'model', label: t('rankModels') },
        { value: 'user', label: t('rankUsers') },
        { value: 'provider', label: t('rankProviders') },
      ],
      onChange: (value) => { selectedDimension = value; paint(); },
    });

    const metric = selectField<UsageMetric>({
      label: t('rankMetric'),
      value: selectedMetric,
      options: [
        { value: 'credits', label: t('metricCredits') },
        { value: 'tokens', label: t('metricTokens') },
        { value: 'requests', label: t('metricRequests') },
      ],
      // A different ranking is a different query, not a different view of
      // the same fifty rows.
      onChange: (value) => { selectedMetric = value; void renderUsage(view); },
    });

    const shape = selectField<ChartShape>({
      label: t('chartShape'),
      value: selectedShape,
      options: [
        { value: 'bar', label: t('chartBar') },
        { value: 'pie', label: t('chartPie') },
      ],
      onChange: (value) => { selectedShape = value; paint(); },
    });

    const controls = el('div', 'oa-ranking-controls');
    controls.appendChild(dimension.element);
    controls.appendChild(metric.element);
    controls.appendChild(shape.element);

    function paint(): void {
      const rows = sets[selectedDimension];

      clear(canvas);
      canvas.appendChild(renderChart({
        shape: selectedShape,
        data: rows.map((row) => ({
          key: row.key,
          label: row.label || row.key || '—',
          value: metricValue(row),
        })),
        format: (value) => selectedMetric === 'credits'
          ? value.toFixed(2)
          : compactNumber(value),
        emptyText: t('nothingInPeriod'),
        otherLabel: t('chartOther'),
      }));
    }
    paint();

    wrap.appendChild(controls);
    wrap.appendChild(canvas);
    return wrap;
  }

  view.body.appendChild(panel(t('secRequestsN', { count: records.total }), renderTable({
    columns: [
      { header: t('colWhen'), cell: (row) => relativeTime(row.started_at) },
      { header: t('colUser'), cell: (row) => row.username || row.user_id },
      { header: t('colModel'), cell: (row) => stacked(row.model_name || '—', row.provider_name), secondary: true },
      { header: t('colIn'), cell: (row) => compactNumber(row.input_tokens), numeric: true, secondary: true },
      { header: t('colOut'), cell: (row) => compactNumber(row.output_tokens), numeric: true, secondary: true },
      { header: t('colCredits'), cell: (row) => compactNumber(row.credits), numeric: true },
      { header: t('colTook'), cell: (row) => `${(row.duration_ms / 1000).toFixed(1)}s`, numeric: true, secondary: true },
      { header: t('colStatus'), cell: (row) => statusBadge(row) },
    ],
    rows: records.records,
    empty: t('noRequestsPeriod'),
    muted: (row) => row.status !== 'ok',
  })));
}

function chart(series: UsagePoint[], bucketMS: number): HTMLElement {
  const wrap = el('div', 'oa-spark');
  if (!series.length) {
    wrap.appendChild(el('span', 'oa-spark-empty', t('noRequestsPeriod')));
    return wrap;
  }
  const peak = Math.max(...series.map((point) => point.total_tokens), 1);
  for (const point of series) {
    const bar = el('div', 'oa-spark-bar');
    bar.style.height = `${Math.max(2, Math.round((point.total_tokens / peak) * 100))}%`;
    bar.title = t('chartTooltip', {
      when: new Date(point.at).toLocaleString(),
      requests: compactNumber(point.requests),
      tokens: compactNumber(point.total_tokens),
    });
    wrap.appendChild(bar);
  }
  wrap.setAttribute('aria-label', t('chartAria', { hours: Math.round(bucketMS / 3600000) }));
  return wrap;
}

// The instance default: what applies to anyone whose group and account say
// nothing. Edited here rather than on the groups page because it is not a
// group.
async function editGlobalPolicy(view: AdminView): Promise<void> {
  let policy = emptyPolicy('global', '');
  try {
    const { policies } = await adminApi.policies();
    policy = policies.find((entry) => entry.scope === 'global') ?? policy;
  } catch {
    // Fall through with the blank policy; saving will create it.
  }

  const rpm = numberField({
    label: t('requestsPerMinute'),
    value: policy.rpm,
    placeholder: t('noLimit'),
    min: 0,
    hint: t('defaultLimitsHint'),
  });
  const tpm = numberField({ label: t('tokensPerMinute'), value: policy.tpm, placeholder: t('noLimit'), min: 0 });

  const windows = (['5h', '1w', '1m'] as QuotaWindowKind[]).map((kind) => {
    const limits = policy.windows[kind];
    return {
      kind,
      enabled: switchField({ label: t('enforceTheWindow', { window: kind }), value: limits?.enabled === true }),
      requests: numberField({ label: t('limitRequests'), value: limits?.requests ?? null, placeholder: t('noLimit'), min: 0 }),
      tokens: numberField({ label: t('limitTokens'), value: limits?.tokens ?? null, placeholder: t('noLimit'), min: 0 }),
      credits: creditsField(limits?.credits ?? null),
    };
  });

  openPanel({
    host: view.host,
    title: t('defaultLimits'),
    confirmLabel: t('save'),
    build: (body) => {
      body.appendChild(el('p', 'oa-field-hint', t('adminsExemptHint')));
      body.appendChild(rpm.element);
      body.appendChild(tpm.element);
      for (const window of windows) {
        body.appendChild(section(t('everyWindow', { window: window.kind })));
        body.appendChild(window.enabled.element);
        body.appendChild(window.requests.element);
        body.appendChild(window.tokens.element);
        body.appendChild(window.credits.element);
      }
    },
    onConfirm: async (handle) => {
      handle.setBusy(true);
      try {
        await adminApi.savePolicy({
          scope: 'global',
          scope_id: '',
          rpm: rpm.value(),
          tpm: tpm.value(),
          windows: Object.fromEntries(windows.map((window) => [window.kind, {
            enabled: window.enabled.value() ? true : null,
            requests: window.requests.value(),
            tokens: window.tokens.value(),
            credits: window.credits.value(),
          }])),
        });
        handle.close();
        view.reload();
      } catch (error) {
        handle.setBusy(false);
        handle.setError(error instanceof ApiError ? error.message : String(error));
      }
    },
  });
}

/** The figure the current metric ranks by, for the chart's own arithmetic. */
function metricValue(row: UsageBreakdown): number {
  switch (selectedMetric) {
    case 'requests':
      return row.requests;
    case 'tokens':
      return row.total_tokens;
    default:
      return row.credits;
  }
}
