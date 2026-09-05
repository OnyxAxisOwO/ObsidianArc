// The dashboard.
//
// Six numbers, one chart and two short lists. There is no attempt to fill the
// screen: an operator opens this to find out whether the server is working
// and what it is costing, and every panel that does not answer one of those
// is a panel they have to look past to find the ones that do.

import { t } from '../i18n';
import { clear, el } from '../ui/dom';
import { selectField } from '../ui/form';
import { badge, badges, compactNumber, relativeTime, renderTable, stacked } from '../ui/table';
import { renderChart, type ChartShape } from '../ui/chart';
import {
  adminApi,
  type UsageBreakdown,
  type UsagePoint,
  type UsageRecord,
  type UsageTotals,
} from './api';
import { failure, type AdminView } from './admin-page';

export async function renderDashboard(view: AdminView): Promise<void> {
  view.setTitle(t('navDashboard'));

  let data;
  try {
    data = await adminApi.dashboard();
  } catch (error) {
    failure(view, error);
    return;
  }

  clear(view.body);

  const counts = data.counts;
  view.body.appendChild(section(t('secInstance'), statGrid([
    { label: t('statUsers'), value: String(counts.users), note: t('nActive', { count: counts.active_users }) },
    { label: t('statProviders'), value: String(counts.providers), note: t('nEnabled', { count: counts.enabled_providers }) },
    { label: t('statModels'), value: String(counts.models), note: t('nEnabled', { count: counts.enabled_models }) },
  ])));

  view.body.appendChild(section(t('secLast24h'), statGrid(usageStats(data.last_24h))));
  view.body.appendChild(section(t('secLast7d'), spark(data.series, data.bucket_ms)));

  // Which models are popular and which accounts are heavy, side by side and
  // in whichever shape reads better. The dashboard is where an operator asks
  // that question first; the usage screen is where they go to break it down.
  if (data.top_models.length || data.top_users.length) {
    view.body.appendChild(section(t('secRanking'),
      popularity(data.top_models, data.top_users)));
  }

  if (data.top_models.length) {
    view.body.appendChild(section(t('secBusiestModels'), renderTable({
      columns: [
        { header: t('colModel'), cell: (row) => row.label || row.key },
        { header: t('colRequests'), cell: (row) => compactNumber(row.requests), numeric: true },
        { header: t('colTokens'), cell: (row) => compactNumber(row.total_tokens), numeric: true, secondary: true },
        { header: t('colCredits'), cell: (row) => compactNumber(row.credits), numeric: true },
      ],
      rows: data.top_models,
      empty: t('nothingYet'),
    })));
  }

  view.body.appendChild(section(t('secRecentRequests'), renderTable({
    columns: [
      { header: t('colWhen'), cell: (row: UsageRecord) => relativeTime(row.started_at) },
      { header: t('colUser'), cell: (row) => row.username || row.user_id },
      { header: t('colModel'), cell: (row) => stacked(row.model_name || '—', row.provider_name), secondary: true },
      { header: t('colTokens'), cell: (row) => compactNumber(row.total_tokens), numeric: true },
      { header: t('colStatus'), cell: (row) => statusBadge(row) },
    ],
    rows: data.recent,
    empty: t('noRequestsYet'),
    muted: (row) => row.status !== 'ok',
  })));
}

function usageStats(totals: UsageTotals): StatSpec[] {
  return [
    {
      label: t('statRequests'),
      value: compactNumber(totals.requests),
      note: totals.errors ? t('nFailed', { count: totals.errors }) : t('allFine'),
    },
    {
      label: t('statTokens'),
      value: compactNumber(totals.total_tokens),
      note: t('tokensInOut', {
        input: compactNumber(totals.input_tokens),
        output: compactNumber(totals.output_tokens),
      }),
    },
    { label: t('statCredits'), value: compactNumber(totals.credits) },
  ];
}

export function statusBadge(row: { status: string; error_code?: string }): HTMLElement {
  switch (row.status) {
    case 'ok':
      return badge(t('statusOk'), 'muted');
    case 'aborted':
      return badge(t('statusStopped'), 'muted');
    case 'rejected':
      return badges(badge(t('statusRefused'), 'danger'), row.error_code ? badge(row.error_code, 'muted') : null);
    default:
      return badges(badge(t('statusFailed'), 'danger'), row.error_code ? badge(row.error_code, 'muted') : null);
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
    wrap.appendChild(el('span', 'oa-spark-empty', t('noRequestsWeek')));
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
  // Referenced so the bucket size is visibly part of the contract rather than
  // an unused field on the response.
  wrap.setAttribute('aria-label', t('chartAria', { hours: Math.round(bucketMS / 3600000) }));
  return wrap;
}

// Remembered across visits, so an operator who prefers a pie gets one.
let dashboardShape: ChartShape = 'bar';

/**
 * Models and accounts, ranked, in one panel.
 *
 * Both charts share a shape control rather than having one each: they are two
 * views of the same question — where is the usage going — and letting them
 * disagree about how to draw it would be a choice with no meaning behind it.
 */
function popularity(models: UsageBreakdown[], users: UsageBreakdown[]): HTMLElement {
  const wrap = el('div', 'oa-ranking');
  const charts = el('div', 'oa-ranking-pair');

  const shape = selectField<ChartShape>({
    label: t('chartShape'),
    value: dashboardShape,
    options: [
      { value: 'bar', label: t('chartBar') },
      { value: 'pie', label: t('chartPie') },
    ],
    onChange: (value) => { dashboardShape = value; paint(); },
  });

  function one(title: string, rows: UsageBreakdown[]): HTMLElement {
    const column = el('div', 'oa-ranking-column');
    column.appendChild(el('h4', 'oa-panel-section-title', title));
    column.appendChild(renderChart({
      shape: dashboardShape,
      // The dashboard ranks by credits: it is the figure an operator is
      // watching when they open this page at all.
      data: rows.map((row) => ({
        key: row.key,
        label: row.label || row.key || '—',
        value: row.credits,
      })),
      format: (value) => value.toFixed(2),
      emptyText: t('nothingYet'),
      max: 6,
      otherLabel: t('chartOther'),
    }));
    return column;
  }

  function paint(): void {
    clear(charts);
    charts.appendChild(one(t('secBusiestModels'), models));
    charts.appendChild(one(t('secTopUsers'), users));
  }
  paint();

  const controls = el('div', 'oa-ranking-controls');
  controls.appendChild(shape.element);
  wrap.appendChild(controls);
  wrap.appendChild(charts);
  return wrap;
}
