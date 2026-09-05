// Usage: what has been spent, by whom, on what — and the instance-wide
// default allowance.
//
// Every figure here is an aggregate over the ledger, so the same page answers
// "what is this costing" and "why did that request fail" without either being
// a separate feature.

import { ApiError } from '../api/client';
import { button, clear, el } from '../ui/dom';
import { openDrawer } from '../ui/drawer';
import { numberField, section, switchField } from '../ui/form';
import { compactNumber, relativeTime, renderTable, stacked } from '../ui/table';
import { adminApi, emptyPolicy, type QuotaWindowKind, type UsagePoint } from './api';
import { section as panel, statGrid, statusBadge } from './dashboard';
import { failure, type AdminView } from './admin-page';

const RANGES = [
  { label: 'Last 24 hours', hours: 24 },
  { label: 'Last 7 days', hours: 24 * 7 },
  { label: 'Last 30 days', hours: 24 * 30 },
];

let selectedRange = 1;

export async function renderUsage(view: AdminView): Promise<void> {
  view.setTitle('Usage');

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
    const option = el('option', null, entry.label);
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
  view.actions.appendChild(button('oa-btn', 'Default limits', () => void editGlobalPolicy(view)));

  clear(view.body);

  const totals = summary.totals;
  view.body.appendChild(panel('Totals', statGrid([
    { label: 'Requests', value: compactNumber(totals.requests), note: totals.errors ? `${totals.errors} failed` : 'all fine' },
    { label: 'Input tokens', value: compactNumber(totals.input_tokens) },
    { label: 'Output tokens', value: compactNumber(totals.output_tokens) },
    { label: 'Reasoning tokens', value: compactNumber(totals.reasoning_tokens) },
    { label: 'Credits', value: compactNumber(totals.credits) },
  ])));

  view.body.appendChild(panel('Over time', chart(summary.series, summary.bucket_ms)));

  view.body.appendChild(panel('By model', renderTable({
    columns: [
      { header: 'Model', cell: (row) => row.label || row.key || '—' },
      { header: 'Requests', cell: (row) => compactNumber(row.requests), numeric: true },
      { header: 'Tokens', cell: (row) => compactNumber(row.total_tokens), numeric: true },
      { header: 'Credits', cell: (row) => compactNumber(row.credits), numeric: true },
      { header: 'Failed', cell: (row) => compactNumber(row.errors), numeric: true, secondary: true },
    ],
    rows: summary.by_model,
    empty: 'Nothing in this period.',
  })));

  view.body.appendChild(panel('By provider', renderTable({
    columns: [
      { header: 'Provider', cell: (row) => row.label || row.key || '—' },
      { header: 'Requests', cell: (row) => compactNumber(row.requests), numeric: true },
      { header: 'Tokens', cell: (row) => compactNumber(row.total_tokens), numeric: true },
      { header: 'Credits', cell: (row) => compactNumber(row.credits), numeric: true },
    ],
    rows: summary.by_provider,
    empty: 'Nothing in this period.',
  })));

  view.body.appendChild(panel(`Requests (${records.total})`, renderTable({
    columns: [
      { header: 'When', cell: (row) => relativeTime(row.started_at) },
      { header: 'User', cell: (row) => row.username || row.user_id },
      { header: 'Model', cell: (row) => stacked(row.model_name || '—', row.provider_name), secondary: true },
      { header: 'In', cell: (row) => compactNumber(row.input_tokens), numeric: true, secondary: true },
      { header: 'Out', cell: (row) => compactNumber(row.output_tokens), numeric: true, secondary: true },
      { header: 'Credits', cell: (row) => compactNumber(row.credits), numeric: true },
      { header: 'Took', cell: (row) => `${(row.duration_ms / 1000).toFixed(1)}s`, numeric: true, secondary: true },
      { header: 'Status', cell: (row) => statusBadge(row) },
    ],
    rows: records.records,
    empty: 'No requests in this period.',
    muted: (row) => row.status !== 'ok',
  })));
}

function chart(series: UsagePoint[], bucketMS: number): HTMLElement {
  const wrap = el('div', 'oa-spark');
  if (!series.length) {
    wrap.appendChild(el('span', 'oa-spark-empty', 'No requests in this period.'));
    return wrap;
  }
  const peak = Math.max(...series.map((point) => point.total_tokens), 1);
  for (const point of series) {
    const bar = el('div', 'oa-spark-bar');
    bar.style.height = `${Math.max(2, Math.round((point.total_tokens / peak) * 100))}%`;
    bar.title = `${new Date(point.at).toLocaleString()} — ${compactNumber(point.requests)} requests, ${compactNumber(point.total_tokens)} tokens`;
    wrap.appendChild(bar);
  }
  wrap.setAttribute('aria-label', `Token usage in ${Math.round(bucketMS / 3600000)}-hour buckets`);
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
    label: 'Requests per minute',
    value: policy.rpm,
    placeholder: 'no limit',
    min: 0,
    hint: 'Applies to everyone whose group and account do not override it.',
  });
  const tpm = numberField({ label: 'Tokens per minute', value: policy.tpm, placeholder: 'no limit', min: 0 });

  const windows = (['5h', '1w', '1m'] as QuotaWindowKind[]).map((kind) => {
    const limits = policy.windows[kind];
    return {
      kind,
      enabled: switchField({ label: `Enforce the ${kind} window`, value: limits?.enabled === true }),
      requests: numberField({ label: 'Requests', value: limits?.requests ?? null, placeholder: 'no limit', min: 0 }),
      tokens: numberField({ label: 'Tokens', value: limits?.tokens ?? null, placeholder: 'no limit', min: 0 }),
      credits: numberField({ label: 'Credits', value: limits?.credits ?? null, placeholder: 'no limit', min: 0, step: 0.1 }),
    };
  });

  openDrawer({
    title: 'Default limits',
    confirmLabel: 'Save',
    build: (body) => {
      body.appendChild(el('p', 'oa-field-hint',
        'Administrators are exempt from all of this unless that is turned off in Settings.'));
      body.appendChild(rpm.element);
      body.appendChild(tpm.element);
      for (const window of windows) {
        body.appendChild(section(`Every ${window.kind}`));
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
