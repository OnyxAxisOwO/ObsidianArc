import { describe, it, expect, beforeEach, afterEach } from 'vitest';
import { createApp, h, nextTick, type App } from 'vue';
import OaLineChart from '../src/components/OaLineChart.vue';
import { changeLanguage, t } from '../src/composables/useI18n';

describe('OaLineChart', () => {
  let app: App | null = null;
  let host: HTMLElement;

  beforeEach(() => {
    document.body.textContent = '';
    host = document.createElement('div');
    document.body.appendChild(host);
  });

  afterEach(async () => {
    app?.unmount();
    app = null;
    await changeLanguage('en');
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

  // The axis end is a word, not a format placeholder like the two offsets
  // beside it, so it has to be translated. The chart is drawn by the public
  // uptime panel.
  it('labels the end of the axis in the reader\'s language', async () => {
    await changeLanguage('en');
    app = createApp({
      render: () =>
        h(OaLineChart, {
          points: [
            { at: Date.now() - 3600000, uptime: 1.0, total: 10 },
            { at: Date.now(), uptime: 1.0, total: 10 },
          ],
          state: 'up',
        }),
    });
    app.mount(host);

    const labels = () => Array.from(host.querySelectorAll('.oa-linechart-label')).map((node) => node.textContent?.trim());
    expect(labels().at(-1)).toBe(t('chartNow'));

    await changeLanguage('zh');
    await nextTick();
    expect(labels().at(-1)).toBe(t('chartNow'));
    expect(labels().at(-1)).not.toBe('Now');
  });

  // Rounding 99.75% to a whole percent prints 100%, which reads as a window
  // without a single failure. Only a true 100% may say so.
  async function hoverTooltip(uptime: number): Promise<string> {
    app = createApp({
      render: () =>
        h(OaLineChart, {
          points: [{ at: Date.now(), uptime, total: 400 }],
          state: 'up',
        }),
    });
    app.mount(host);
    host.querySelector('.oa-linechart-point-group')!.dispatchEvent(new Event('mouseenter'));
    await nextTick();
    return host.querySelector('.oa-linechart-hover-info')?.textContent ?? '';
  }

  it('does not round a window with failures up to 100%', async () => {
    const info = await hoverTooltip(0.9975);
    expect(info).toContain('99.7%');
    expect(info).not.toContain('100%');
  });

  it('says 100% only for a window with no failures', async () => {
    const info = await hoverTooltip(1);
    expect(info).toContain('100%');
  });
});
