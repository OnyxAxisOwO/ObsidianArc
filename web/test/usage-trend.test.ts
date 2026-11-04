// The figure over the usage curve follows the hovered bucket, falls back to
// the range's total, and lets go of the bucket when the line changes metric:
// an index picked on the requests line read as a figure of credits is a
// number nobody spent.

import { afterEach, describe, expect, it } from 'vitest';
import { createApp, h, nextTick, shallowRef, type App } from 'vue';
import type { UsagePoint, UsageTotals } from '../src/admin/api';
import { t } from '../src/composables/useI18n';
import UsageTrend from '../src/views/admin/usage/UsageTrend.vue';

const HOUR = 3_600_000;
const START = Date.UTC(2026, 9, 18);

function bucket(index: number, requests: number, credits: number): UsagePoint {
  return {
    at: START + index * HOUR, requests, input_tokens: 0, output_tokens: 0, reasoning_tokens: 0, total_tokens: 0,
    credits, errors: 0, users: 1, models: 1, duration_ms: 0,
  };
}

const series = [bucket(0, 3, 30), bucket(1, 5, 50)];
const totals = { ...bucket(0, 8, 80), users: 2 } as UsageTotals;

let app: App | undefined;
let host: HTMLElement | undefined;

afterEach(() => {
  app?.unmount();
  app = undefined;
  host?.remove();
  host = undefined;
});

function mountTrend() {
  const metric = shallowRef<'requests' | 'credits'>('requests');
  host = document.createElement('div');
  document.body.append(host);
  app = createApp({
    setup() {
      return () => h(UsageTrend, {
        series,
        points: series.map((point) => ({ at: point.at, value: point[metric.value] })),
        totals,
        metric: metric.value,
        format: (value: number) => `#${value}`,
        bucketMs: HOUR,
        offsetMs: 0,
      });
    },
  });
  app.mount(host);
  return { metric, root: host };
}

const figure = (root: HTMLElement) => root.querySelector('.oa-viz-summary strong')!.textContent;
const caption = (root: HTMLElement) => root.querySelector('.oa-viz-summary span')!.textContent;

describe('UsageTrend', () => {
  it('reads the hovered bucket, and the range total when nothing is hovered', async () => {
    const { root } = mountTrend();
    expect(figure(root)).toBe('#8');
    expect(caption(root)).toBe(t('trendTotal'));

    root.querySelector('.oa-plot-area')!.dispatchEvent(new MouseEvent('pointermove', { bubbles: true, clientX: 0 }));
    await nextTick();
    expect(figure(root)).toBe('#3');
    expect(caption(root)).not.toBe(t('trendTotal'));
  });

  it('lets go of the hovered bucket when the metric changes', async () => {
    const { metric, root } = mountTrend();
    root.querySelector('.oa-plot-area')!.dispatchEvent(new MouseEvent('pointermove', { bubbles: true, clientX: 0 }));
    await nextTick();
    expect(figure(root)).toBe('#3');

    metric.value = 'credits';
    await nextTick();
    await nextTick();
    expect(figure(root)).toBe('#80');
    expect(caption(root)).toBe(t('trendTotal'));
  });

  it('says there was nothing to draw when every bucket is empty', () => {
    host = document.createElement('div');
    document.body.append(host);
    app = createApp({
      setup() {
        return () => h(UsageTrend, {
          series: [], points: [], totals: null, metric: 'requests',
          format: String, bucketMs: HOUR, offsetMs: 0,
        });
      },
    });
    app.mount(host);
    expect(host.querySelector('.oa-viz-empty')?.textContent).toBe(t('noRequestsPeriod'));
    expect(figure(host)).toBe('0');
  });
});
