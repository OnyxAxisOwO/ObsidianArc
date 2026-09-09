import { describe, it, expect, beforeEach, afterEach } from 'vitest';
import { createApp, h, type App } from 'vue';
import OaLineChart from '../src/components/OaLineChart.vue';

describe('OaLineChart', () => {
  let app: App | null = null;
  let host: HTMLElement;

  beforeEach(() => {
    document.body.textContent = '';
    host = document.createElement('div');
    document.body.appendChild(host);
  });

  afterEach(() => {
    app?.unmount();
    app = null;
    document.body.textContent = '';
  });

  it('renders empty message when there are no traffic samples', () => {
    app = createApp({
      render: () =>
        h(OaLineChart, {
          points: [
            { at: 1000, uptime: null, total: 0 },
            { at: 2000, uptime: null, total: 0 },
          ],
          state: 'unknown',
        }),
    });
    app.mount(host);

    const emptyText = host.querySelector('.oa-linechart-empty-text');
    expect(emptyText).not.toBeNull();
    expect(host.querySelector('path')).toBeNull();
  });

  it('renders line and area paths when points have traffic data', () => {
    const now = Date.now();
    app = createApp({
      render: () =>
        h(OaLineChart, {
          points: [
            { at: now - 7200000, uptime: 1.0, total: 10 },
            { at: now - 3600000, uptime: 0.8, total: 5 },
            { at: now, uptime: 0.95, total: 20 },
          ],
          state: 'up',
        }),
    });
    app.mount(host);

    expect(host.querySelector('.oa-linechart-empty-text')).toBeNull();
    const paths = host.querySelectorAll('path');
    expect(paths.length).toBeGreaterThanOrEqual(2); // area path + line path

    const dots = host.querySelectorAll('.oa-linechart-dot');
    expect(dots.length).toBe(3);
  });
});
