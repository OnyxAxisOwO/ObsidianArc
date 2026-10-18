// The chart's select event is off by default and no caller turns it on yet.
// These tests pin the contract the prop promises, so a drill-down added later
// can rely on it: a click on a bar selects its key only when selectable is set.

import { afterEach, describe, expect, it } from 'vitest';
import { createApp, h, nextTick, type App } from 'vue';
import OaChart from '../src/components/OaChart.vue';
import type { Slice } from '../src/lib/chart';

// jsdom has no matchMedia, and the chart reads the colour scheme from it.
if (!window.matchMedia) {
  window.matchMedia = ((query: string) => ({
    matches: false,
    media: query,
    addEventListener: () => {},
    removeEventListener: () => {},
  })) as unknown as typeof window.matchMedia;
}

const slices: Slice[] = [
  { key: 'alpha', label: 'Alpha', value: 3 },
  { key: 'beta', label: 'Beta', value: 1 },
];

describe('OaChart selection', () => {
  let app: App | null = null;

  afterEach(() => {
    app?.unmount();
    app = null;
  });

  // Not mountComponent: the tests use the screen the moment it is mounted, without awaiting, and read its first render.
  function mount(selectable: boolean, picked: string[]): HTMLElement {
    const host = document.createElement('div');
    document.body.appendChild(host);
    app = createApp({
      render: () =>
        h(OaChart, {
          shape: 'bar',
          data: slices,
          format: (value: number) => String(value),
          emptyText: 'none',
          selectable,
          onSelect: (key: string) => picked.push(key),
        }),
    });
    app.mount(host);
    return host;
  }

  it('emits the key of the bar that was clicked when selectable', async () => {
    const picked: string[] = [];
    const host = mount(true, picked);
    // The legend rows carry the same class, so only the bars are counted here.
    const hits = host.querySelectorAll('rect.oa-chart-hit');
    expect(hits.length).toBe(2);

    (hits[0] as HTMLElement).dispatchEvent(new MouseEvent('click', { bubbles: true }));
    await nextTick();
    expect(picked).toEqual(['alpha']);
  });

  it('draws no hit targets and emits nothing when not selectable', async () => {
    const picked: string[] = [];
    const host = mount(false, picked);
    expect(host.querySelectorAll('.oa-chart-hit').length).toBe(0);

    host.querySelector('rect')!.dispatchEvent(new MouseEvent('click', { bubbles: true }));
    await nextTick();
    expect(picked).toEqual([]);
  });
});
