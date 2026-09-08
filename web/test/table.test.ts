import { describe, it, expect, beforeEach, afterEach } from 'vitest';
import { createApp, h, nextTick, ref, type App } from 'vue';
import OaTable from '../src/components/OaTable.vue';
import type { SortState } from '../src/components/table-types';
import { relativeTime } from '../src/lib/format';

describe('relativeTime', () => {
  // Every phrase it can produce is past tense, so a moment that has not
  // happened yet used to come out as "just now": a reset card expiring in a
  // month was displayed as one expiring this second.
  it('does not describe a future moment in the past tense', () => {
    const month = Date.now() + 30 * 24 * 3600 * 1000;
    expect(relativeTime(month)).toBe(new Date(month).toLocaleString());
  });

  it('still reads the past as it always did', () => {
    expect(relativeTime(0)).toBe('—');
    // A minute ago is a phrase, not a date.
    const minute = Date.now() - 90 * 1000;
    expect(relativeTime(minute)).not.toBe(new Date(minute).toLocaleString());
  });
});

describe('OaTable sort cycling', () => {
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

  it('cycles sort through ascending -> descending -> reset (null) on 3rd click', async () => {
    const sort = ref<SortState | null>(null);
    const emitted: Array<SortState | null> = [];

    app = createApp({
      render: () => h(OaTable, {
        columns: [{ key: 'name', header: 'Name', sort: (r: { name: string }) => r.name }],
        rows: [{ name: 'Beta' }, { name: 'Alpha' }],
        empty: 'No items',
        sort: sort.value,
        reorderable: true,
        'onSort': (next: SortState | null) => {
          sort.value = next;
          emitted.push(next);
        },
      }),
    });
    app.mount(host);

    const th = host.querySelector<HTMLTableCellElement>('th.sortable')!;
    expect(th).not.toBeNull();

    // 1st click: ascending
    th.click();
    await nextTick();
    expect(sort.value).toEqual({ column: 0, descending: false });

    // 2nd click: descending
    th.click();
    await nextTick();
    expect(sort.value).toEqual({ column: 0, descending: true });

    // 3rd click: resets to null so drag-and-drop becomes active again
    th.click();
    await nextTick();
    expect(sort.value).toBeNull();
    expect(emitted).toEqual([
      { column: 0, descending: false },
      { column: 0, descending: true },
      null,
    ]);
  });
});
