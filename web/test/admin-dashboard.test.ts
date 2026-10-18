import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import { createApp, h, nextTick, type App } from 'vue';
import { createMemoryHistory, createRouter } from 'vue-router';
import { adminApi } from '../src/admin/api';
import AdminDashboard from '../src/views/admin/AdminDashboard.vue';
import AdminDashboardTrend from '../src/views/admin/AdminDashboardTrend.vue';
import { provideAdminView } from '../src/views/admin/adminView';
import { changeLanguage, t, tn } from '../src/composables/useI18n';
import { dashboardFixture, emptyTotals } from './fixtures/dashboard';
import { settle, button } from './helpers';

let app: App | undefined;
let host: HTMLElement;
let actions: HTMLElement;
beforeEach(async () => {
  await changeLanguage('en');
  host = document.createElement('div');
  actions = document.createElement('div');
  document.body.append(host, actions);
});
afterEach(() => { app?.unmount(); app = undefined; vi.restoreAllMocks(); });
async function mountDashboard(): Promise<void> {
  const router = createRouter({ history: createMemoryHistory(), routes: [{ path: '/:pathMatch(.*)*', component: { render: () => null } }] });
  app = createApp({ setup() {
    provideAdminView({ actionsHost: actions, setTitle() {}, reload() {}, params: [] });
    return () => h(AdminDashboard);
  } });
  app.use(router);
  app.mount(host);
  await settle();
}

describe('dashboard overview', () => {
  it('switches the ranking without losing names, exact figures or detailed request counts', async () => {
    vi.spyOn(adminApi, 'dashboard').mockResolvedValue(dashboardFixture());
    await mountDashboard();
    const ranking = host.querySelector('#secBusiestModels')!;
    expect(ranking.textContent).toContain('Claude Sonnet 4.5');
    expect(ranking.querySelectorAll('.oa-dashboard-rank-list li')).toHaveLength(5);
    expect(ranking.querySelector('details')?.textContent).toContain('840,000');
    button(ranking, t('statUsers')).click(); await nextTick();
    expect(ranking.querySelector('.oa-dashboard-rank-list')?.textContent).toContain('onyx');
    expect(ranking.textContent).not.toContain('Claude Sonnet 4.5');
    await changeLanguage('zh');
    expect(host.textContent).toContain('概览');
    expect(button(ranking, t('statUsers')).getAttribute('aria-pressed')).toBe('true');
  });

  // Tokens, not credits. A model priced at zero carries real load and cost
  // nothing, so a chart of credits left it off entirely; this one puts it
  // first when it has the most tokens. The pair below is chosen so the two
  // metrics disagree about the order.
  it('ranks by tokens, so a model priced at zero still shows its load', async () => {
    const fixture = dashboardFixture();
    fixture.top_models = [
      { ...fixture.top_models[0]!, key: 'priced', label: 'Priced model', total_tokens: 1_000_000, credits: 900_000 },
      { ...fixture.top_models[1]!, key: 'free', label: 'Free model', total_tokens: 9_000_000, credits: 0 },
    ];
    const load = vi.spyOn(adminApi, 'dashboard').mockResolvedValue(fixture);
    await mountDashboard();

    expect(load).toHaveBeenCalledWith('tokens');
    const rows = [...host.querySelectorAll('#secBusiestModels .oa-dashboard-rank-list li')];
    expect(rows.map((row) => row.querySelector('.oa-dashboard-rank-label span')?.textContent)).toEqual(['Free model', 'Priced model']);
    expect(rows[0]!.textContent).toContain('90.0%');
  });

  // The busiest hour by requests and the busiest by tokens are often different
  // hours — many small turns against a few long ones — so the pair below
  // disagrees, and the caption has to follow the switch.
  it('switches the heatmap between requests and tokens', async () => {
    const fixture = dashboardFixture();
    fixture.heatmap = [{ weekday: 4, hour: 21, requests: 64, total_tokens: 100_000 }, { weekday: 1, hour: 10, requests: 12, total_tokens: 900_000 }];
    vi.spyOn(adminApi, 'dashboard').mockResolvedValue(fixture);
    await mountDashboard();
    const card = host.querySelector('#secWhen')!;
    const caption = () => card.querySelector('.oa-heatmap-caption')?.textContent ?? '';
    expect(caption()).toContain(t('boardRequests', { count: '64' }));

    button(card, t('statTokens')).click(); await nextTick();
    expect(button(card, t('statTokens')).getAttribute('aria-pressed')).toBe('true');
    expect(caption()).toContain(t('boardTokens', { count: '900k' }));
  });

  it('keeps the last snapshot on refresh failure and allows a retry', async () => {
    const load = vi.spyOn(adminApi, 'dashboard').mockResolvedValueOnce(dashboardFixture()).mockRejectedValueOnce(new Error('Temporarily unavailable')).mockResolvedValueOnce({ ...dashboardFixture(), counts: { ...dashboardFixture().counts, users: 200 } });
    await mountDashboard();
    button(host, t('refresh')).click(); await settle();
    expect(host.textContent).toContain('Temporarily unavailable');
    expect(host.querySelector('.oa-dashboard-resource-copy')?.textContent).toContain('176');
    button(host, t('refresh')).click(); await settle();
    expect(host.textContent).not.toContain('Temporarily unavailable');
    expect(host.querySelector('.oa-dashboard-resource-copy')?.textContent).toContain('200');
    expect(load).toHaveBeenCalledTimes(3);
  });

  it('shows empty states and never invents a success rate for an unused instance', async () => {
    vi.spyOn(adminApi, 'dashboard').mockResolvedValue({ ...dashboardFixture(), last_24h: { ...emptyTotals }, prev_24h: { ...emptyTotals }, last_7d: { ...emptyTotals }, series: [], heatmap: [], top_models: [], top_users: [], recent: [] });
    await mountDashboard();
    expect(host.textContent).toContain(t('noRequestsWeek'));
    expect(host.textContent).toContain(t('noRequestsYet'));
    expect(host.textContent).toContain(t('heatmapQuiet'));
    expect(host.textContent).not.toContain('100%');
    expect(host.textContent).not.toContain('NaN');
    expect(host.querySelector('[role="slider"]')).toBeNull();
    // Nothing yesterday and nothing today is no movement, not a fall.
    expect(host.querySelector('.oa-delta')).toBeNull();
  });

  // Each of the day's figures says which way it moved against the day before,
  // and the ones where up is worse — failures, waiting — say so in the colour
  // kept for that.
  it('compares the day with the one before it', async () => {
    const fixture = dashboardFixture();
    vi.spyOn(adminApi, 'dashboard').mockResolvedValue(fixture);
    await mountDashboard();
    const metrics = host.querySelector('.oa-dashboard-metrics')!;
    // 4906 against 4460 is a tenth up.
    expect(metrics.querySelector('.oa-delta')?.textContent).toContain('10%');
    expect(metrics.querySelector('.oa-delta')?.classList.contains('up')).toBe(true);
    const failures = host.querySelector('#secHealth .has-errors .oa-delta')!;
    expect(failures.classList.contains('worse')).toBe(true);
    expect(host.querySelector('#secHealth .oa-dashboard-health-rate strong')?.textContent).toBe('78.2%');
  });

  it('names who used a model under its bar', async () => {
    vi.spyOn(adminApi, 'dashboard').mockResolvedValue(dashboardFixture());
    await mountDashboard();
    const first = host.querySelector('#secBusiestModels .oa-dashboard-rank-list li')!;
    expect(first.querySelector('.oa-dashboard-rank-reach')?.textContent).toContain(tn(40, 'boardUsersOne', 'boardUsersOther', { count: 40 }));
  });
});

describe('dashboard trend', () => {
  it('explores real bucket values with the keyboard and switches to token totals', async () => {
    const data = dashboardFixture();
    app = createApp(AdminDashboardTrend, { series: data.series, totals: data.last_7d, bucketMs: data.bucket_ms });
    app.mount(host); await nextTick();
    const slider = host.querySelector<HTMLElement>('[role="slider"]')!;
    slider.focus();
    slider.dispatchEvent(new KeyboardEvent('keydown', { key: 'Home', bubbles: true })); await nextTick();
    expect(slider.getAttribute('aria-valuenow')).toBe('1');
    expect(host.querySelector('.oa-dashboard-trend-summary strong')?.textContent).toBe('120');
    slider.dispatchEvent(new KeyboardEvent('keydown', { key: 'ArrowLeft', bubbles: true })); await nextTick();
    expect(slider.getAttribute('aria-valuenow')).toBe('1');
    slider.dispatchEvent(new KeyboardEvent('keydown', { key: 'ArrowRight', bubbles: true })); await nextTick();
    expect(host.querySelector('.oa-dashboard-trend-summary strong')?.textContent).toBe('180');
    slider.blur();
    button(host, t('statTokens')).click(); await nextTick();
    expect(host.textContent).toContain(t('dashboardWeekTotal'));
    expect(button(host, t('statTokens')).getAttribute('aria-pressed')).toBe('true');
    expect(host.querySelector('.oa-plot-line')?.getAttribute('d')).not.toMatch(/NaN|Infinity/);
  });

  it('renders one bucket without dividing by zero and distinguishes zero tokens from zero requests', async () => {
    // Long before the week the chart fills in, so the one bucket is drawn alone.
    const point = { ...emptyTotals, at: Date.UTC(2020, 0, 1), requests: 1 };
    app = createApp(AdminDashboardTrend, { series: [point], totals: point, bucketMs: 21600000 });
    app.mount(host); await nextTick();
    // Centred, at the top of a scale whose ceiling is that one request.
    expect(host.querySelector('.oa-plot-line')?.getAttribute('d')).toBe('M 360.00 0.00');
    button(host, t('statTokens')).click(); await nextTick();
    expect(host.textContent).toContain(t('dashboardNoTokens'));
    expect(host.querySelector('[role="slider"]')).toBeNull();
  });

  // A week with a silent night in it: the buckets nobody used come back as
  // zeros rather than being bridged by a line that suggests traffic tapered.
  it('puts the empty buckets of the week back', async () => {
    const offset = -new Date().getTimezoneOffset() * 60_000;
    const step = 21_600_000;
    const now = Date.now();
    const last = now - (((now + offset) % step) + step) % step;
    const series = [{ ...emptyTotals, at: last - 4 * step, requests: 5 }, { ...emptyTotals, at: last, requests: 7 }];
    app = createApp(AdminDashboardTrend, { series, totals: { ...emptyTotals, requests: 12 }, bucketMs: step });
    app.mount(host); await nextTick();
    const slider = host.querySelector<HTMLElement>('[role="slider"]')!;
    // Seven days of six-hour buckets, inclusive of both ends.
    expect(Number(slider.getAttribute('aria-valuemax'))).toBeGreaterThanOrEqual(28);
    slider.focus();
    slider.dispatchEvent(new KeyboardEvent('keydown', { key: 'End', bubbles: true })); await nextTick();
    expect(host.querySelector('.oa-dashboard-trend-summary strong')?.textContent).toBe('7');
    slider.dispatchEvent(new KeyboardEvent('keydown', { key: 'ArrowLeft', bubbles: true })); await nextTick();
    expect(host.querySelector('.oa-dashboard-trend-summary strong')?.textContent).toBe('0');
  });
});
