// The value under the pointer must survive a refresh that brings the same
// buckets back. The page rebuilds the series every fifteen seconds, so a
// readout cleared by a new array is cleared for no reason the reader can see;
// a different set of buckets is the case that really invalidates the index.

import { afterEach, describe, expect, it } from 'vitest';
import { createApp, h, nextTick, ref, shallowRef, type App } from 'vue';
import UsagePlot from '../src/views/admin/usage/UsagePlot.vue';
import type { PlotPoint } from '../src/views/admin/usage/shape';

const HOUR = 3_600_000;
const START = Date.UTC(2026, 9, 18);

function buckets(values: number[], start = START): PlotPoint[] {
  return values.map((value, index) => ({ at: start + index * HOUR, value }));
}

let app: App | undefined;
let host: HTMLElement | undefined;

afterEach(() => {
  app?.unmount();
  app = undefined;
  host?.remove();
  host = undefined;
});

function mountPlot(initial: PlotPoint[]) {
  const data = shallowRef(initial);
  const active = ref<number | null>(null);
  host = document.createElement('div');
  document.body.append(host);
  app = createApp({
    setup() {
      return () => h(UsagePlot, {
        points: data.value,
        bucketMs: HOUR,
        offsetMs: 0,
        format: (value: number) => String(value),
        label: 'Requests',
        active: active.value,
        'onUpdate:active': (next: number | null) => { active.value = next; },
      });
    },
  });
  app.mount(host);
  return { data, active, root: host };
}

function point(root: HTMLElement): void {
  root.querySelector('.oa-plot-area')!.dispatchEvent(new MouseEvent('pointermove', { bubbles: true, clientX: 0 }));
}

describe('UsagePlot readout across a refresh', () => {
  it('keeps the hovered bucket when the refresh brings the same buckets as a new array', async () => {
    const { data, active, root } = mountPlot(buckets([3, 5, 2]));
    point(root);
    await nextTick();
    expect(active.value).toBe(0);
    expect(root.querySelector('.oa-plot-bubble')).not.toBeNull();

    data.value = buckets([4, 6, 2]);
    await nextTick();

    expect(active.value).toBe(0);
    expect(root.querySelector('.oa-plot-bubble')).not.toBeNull();
  });

  it('clears the hovered bucket when the buckets themselves change', async () => {
    const { data, active, root } = mountPlot(buckets([3, 5, 2]));
    point(root);
    await nextTick();

    data.value = buckets([3, 5, 2], START + HOUR);
    await nextTick();

    expect(active.value).toBeNull();
    expect(root.querySelector('.oa-plot-bubble')).toBeNull();
  });
});
