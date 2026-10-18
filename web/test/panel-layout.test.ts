// The side panel's column: the width the reader dragged it to has to survive a
// re-render of the panel, and a full-screen toggle has to leave the columns
// beside it as it found them, even when it is toggled again mid-animation.

import { afterEach, describe, expect, it } from 'vitest';
import { createApp, h, nextTick, ref, type App as VueApp } from 'vue';
import OaPanel from '../src/components/OaPanel.vue';
import { providePanelHost } from '../src/composables/usePanelHost';

const WIDTH_KEY = 'obsidian-arc-panel-width';
// The zoom animation is 340 ms, with 20 ms over.
const PAST_THE_ZOOM_MS = 380;
const wait = (ms: number): Promise<void> => new Promise((resolve) => setTimeout(resolve, ms));

let app: VueApp | null = null;
let host: HTMLElement | null = null;

afterEach(() => {
  app?.unmount();
  app = null;
  host?.remove();
  host = null;
  localStorage.clear();
});

describe('OaPanel width', () => {
  it('keeps a dragged width when the panel re-renders', async () => {
    localStorage.setItem(WIDTH_KEY, '520');
    const busy = ref(false);

    const root = document.createElement('div');
    document.body.appendChild(root);
    host = root;
    app = createApp({
      setup() {
        const row = document.createElement('div');
        root.appendChild(row);
        providePanelHost(ref(row));
        return () => h('div', [h(OaPanel, { title: 'Panel', busy: busy.value })]);
      },
    });
    app.mount(root);
    await nextTick();

    const aside = root.querySelector<HTMLElement>('.oa-panel')!;
    expect(aside.style.getPropertyValue('--oa-panel-width')).toBe('520px');

    // What OaResizer does on a drag: it writes the property to the column itself.
    aside.style.setProperty('--oa-panel-width', '600px');

    busy.value = true;
    await nextTick();
    expect(aside.style.getPropertyValue('--oa-panel-width')).toBe('600px');
  });

  it('applies the stored width when the row arrives after the panel has mounted', async () => {
    localStorage.setItem(WIDTH_KEY, '520');
    const root = document.createElement('div');
    document.body.appendChild(root);
    host = root;
    const row = ref<HTMLElement | null>(null);
    app = createApp({
      setup() {
        providePanelHost(row);
        return () => h('div', [h(OaPanel, { title: 'Panel' })]);
      },
    });
    app.mount(root);
    await nextTick();

    const holder = document.createElement('div');
    root.appendChild(holder);
    row.value = holder;
    await nextTick();

    const aside = root.querySelector<HTMLElement>('.oa-panel')!;
    expect(aside.style.getPropertyValue('--oa-panel-width')).toBe('520px');
  });
});

describe('OaPanel full screen', () => {
  function mountWithSibling(): { sibling: HTMLElement; panel: { value: InstanceType<typeof OaPanel> | null } } {
    const root = document.createElement('div');
    document.body.appendChild(root);
    host = root;
    const sibling = document.createElement('div');
    const panel = ref<InstanceType<typeof OaPanel> | null>(null);
    app = createApp({
      setup() {
        const row = document.createElement('div');
        root.appendChild(row);
        row.appendChild(sibling);
        providePanelHost(ref(row));
        return () => h('div', [h(OaPanel, { ref: panel, title: 'Panel' })]);
      },
    });
    app.mount(root);
    return { sibling, panel };
  }

  it('gives the columns beside it back their own widths after a second toggle mid-animation', async () => {
    const { sibling, panel } = mountWithSibling();
    await nextTick();

    panel.value!.toggleFullscreen();
    await wait(50);
    panel.value!.toggleFullscreen();
    await wait(PAST_THE_ZOOM_MS);

    expect(sibling.style.flex).toBe('');
  });

  it('gives the columns beside it back their own widths when it goes mid-animation', async () => {
    const { sibling, panel } = mountWithSibling();
    await nextTick();

    panel.value!.toggleFullscreen();
    expect(sibling.style.flex).not.toBe('');

    app?.unmount();
    app = null;
    expect(sibling.style.flex).toBe('');
  });
});

describe('OaPanel beside two columns', () => {
  // jsdom has no layout. A column reports a narrower width once a column in its
  // row is pinned, as a real row does when the panel leaves the flow, so these
  // tests show whether every width is read before the first pin is written.
  function mountWithColumns(): { columns: HTMLElement[]; panel: { value: InstanceType<typeof OaPanel> | null } } {
    const root = document.createElement('div');
    document.body.appendChild(root);
    host = root;
    const columns = [document.createElement('div'), document.createElement('div')];
    const panel = ref<InstanceType<typeof OaPanel> | null>(null);
    app = createApp({
      setup() {
        const row = document.createElement('div');
        root.appendChild(row);
        for (const column of columns) row.appendChild(column);
        providePanelHost(ref(row));
        return () => h('div', [h(OaPanel, { ref: panel, title: 'Panel' })]);
      },
    });
    app.mount(root);
    const anyPinned = (): boolean => columns.some((column) => column.style.flex !== '');
    for (const column of columns) {
      column.getBoundingClientRect = () => ({ width: anyPinned() ? 120 : 300 }) as DOMRect;
    }
    return { columns, panel };
  }

  it('pins each column to the width it had before any of them was pinned, going full screen', async () => {
    const { columns, panel } = mountWithColumns();
    await nextTick();

    panel.value!.toggleFullscreen();
    expect(columns.map((column) => column.style.flex)).toEqual(['0 0 300px', '0 0 300px']);
  });

  it('pins each column to the width it had before any of them was pinned, leaving full screen', async () => {
    const { columns, panel } = mountWithColumns();
    await nextTick();

    panel.value!.toggleFullscreen();
    await wait(PAST_THE_ZOOM_MS);
    panel.value!.toggleFullscreen();
    expect(columns.map((column) => column.style.flex)).toEqual(['0 0 300px', '0 0 300px']);
  });
});
