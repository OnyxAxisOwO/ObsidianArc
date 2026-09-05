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
import { numberField, section, switchField } from '../ui/form';
import { compactNumber, relativeTime, renderTable, stacked } from '../ui/table';
import { adminApi, emptyPolicy, type QuotaWindowKind, type UsagePoint } from './api';
import { section as panel, statGrid, statusBadge } from './dashboard';
import { failure, type AdminView } from './admin-page';

const RANGES: Array<{ label: StringKey; hours: number }> = [
  { label: 'rangeDay', hours: 24 },
  { label: 'rangeWeek', hours: 24 * 7 },
  { label: 'rangeMonth', hours: 24 * 30 },
];

let selectedRange = 1;

export async function renderUsage(view: AdminView): Promise<void> {
  view.setTitle(t('usageTitle'));

  const since = Date.now() - RANGES[selectedRange]!.hours * 3600_000;
  const query = `?since=${since}`;

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
      credits: numberField({ label: t('limitCredits'), value: limits?.credits ?? null, placeholder: t('noLimit'), min: 0, step: 0.1 }),
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
