// The dashboard.
//
// Six numbers, one chart and two short lists. There is no attempt to fill the
// screen: an operator opens this to find out whether the server is working
// and what it is costing, and every panel that does not answer one of those
// is a panel they have to look past to find the ones that do.

import { clear, el } from '../ui/dom';
import { badge, badges, compactNumber, relativeTime, renderTable, stacked } from '../ui/table';
import { adminApi, type UsagePoint, type UsageRecord, type UsageTotals } from './api';
import { failure, type AdminView } from './admin-page';

export async function renderDashboard(view: AdminView): Promise<void> {
  view.setTitle('Dashboard');

  let data;
  try {
    data = await adminApi.dashboard();
  } catch (error) {
    failure(view, error);
    return;
  }

  clear(view.body);

  const counts = data.counts;
  view.body.appendChild(section('Instance', statGrid([
    { label: 'Users', value: String(counts.users), note: `${counts.active_users} active` },
    { label: 'Providers', value: String(counts.providers), note: `${counts.enabled_providers} enabled` },
    { label: 'Models', value: String(counts.models), note: `${counts.enabled_models} enabled` },
  ])));

  view.body.appendChild(section('Last 24 hours', statGrid(usageStats(data.last_24h))));
  view.body.appendChild(section('Last 7 days', spark(data.series, data.bucket_ms)));

  if (data.top_models.length) {
    view.body.appendChild(section('Busiest models, last 7 days', renderTable({
      columns: [
        { header: 'Model', cell: (row) => row.label || row.key },
        { header: 'Requests', cell: (row) => compactNumber(row.requests), numeric: true },
        { header: 'Tokens', cell: (row) => compactNumber(row.total_tokens), numeric: true, secondary: true },
        { header: 'Credits', cell: (row) => compactNumber(row.credits), numeric: true },
      ],
      rows: data.top_models,
      empty: 'Nothing yet.',
    })));
  }

  view.body.appendChild(section('Recent requests', renderTable({
    columns: [
      { header: 'When', cell: (row: UsageRecord) => relativeTime(row.started_at) },
      { header: 'User', cell: (row) => row.username || row.user_id },
      { header: 'Model', cell: (row) => stacked(row.model_name || '—', row.provider_name), secondary: true },
      { header: 'Tokens', cell: (row) => compactNumber(row.total_tokens), numeric: true },
      { header: 'Status', cell: (row) => statusBadge(row) },
    ],
    rows: data.recent,
    empty: 'No requests yet.',
    muted: (row) => row.status !== 'ok',
  })));
}

function usageStats(totals: UsageTotals): StatSpec[] {
  return [
    { label: 'Requests', value: compactNumber(totals.requests), note: totals.errors ? `${totals.errors} failed` : 'all fine' },
    { label: 'Tokens', value: compactNumber(totals.total_tokens), note: `${compactNumber(totals.input_tokens)} in · ${compactNumber(totals.output_tokens)} out` },
    { label: 'Credits', value: compactNumber(totals.credits) },
  ];
}

export function statusBadge(row: { status: string; error_code?: string }): HTMLElement {
  switch (row.status) {
    case 'ok':
      return badge('ok', 'muted');
    case 'aborted':
      return badge('stopped', 'muted');
    case 'rejected':
      return badges(badge('refused', 'danger'), row.error_code ? badge(row.error_code, 'muted') : null);
    default:
      return badges(badge('failed', 'danger'), row.error_code ? badge(row.error_code, 'muted') : null);
  }
}

export interface StatSpec {
  label: string;
  value: string;
  note?: string;
}

export function statGrid(stats: StatSpec[]): HTMLElement {
  const grid = el('div', 'oa-stat-grid');
  for (const stat of stats) {
    const card = el('div', 'oa-stat');
    card.appendChild(el('span', 'oa-stat-label', stat.label));
    card.appendChild(el('span', 'oa-stat-value', stat.value));
    if (stat.note) card.appendChild(el('span', 'oa-stat-note', stat.note));
    grid.appendChild(card);
  }
  return grid;
}

export function section(title: string, content: HTMLElement): HTMLElement {
  const wrap = el('div', 'oa-admin-section');
  wrap.appendChild(el('h2', 'oa-admin-section-title', title));
  wrap.appendChild(content);
  return wrap;
}

// A bar per bucket, scaled to the tallest. Drawn with divs because a charting
// library would be many times the size of the one chart it draws, and this
// chart has no interaction to justify it.
function spark(series: UsagePoint[], bucketMS: number): HTMLElement {
  const wrap = el('div', 'oa-spark');
  if (!series.length) {
    wrap.appendChild(el('span', 'oa-spark-empty', 'No requests in the last seven days.'));
    return wrap;
  }

  const peak = Math.max(...series.map((point) => point.total_tokens), 1);
  for (const point of series) {
    const bar = el('div', 'oa-spark-bar');
    bar.style.height = `${Math.max(2, Math.round((point.total_tokens / peak) * 100))}%`;
    bar.title = `${new Date(point.at).toLocaleString()} — ${compactNumber(point.requests)} requests, ${compactNumber(point.total_tokens)} tokens`;
    wrap.appendChild(bar);
  }
  // Referenced so the bucket size is visibly part of the contract rather than
  // an unused field on the response.
  wrap.setAttribute('aria-label', `Token usage in ${Math.round(bucketMS / 3600000)}-hour buckets`);
  return wrap;
}
