import { describe, it, expect, beforeEach, afterEach } from 'vitest';
import { createApp, h, nextTick, ref, type App } from 'vue';
import OaTable from '../src/components/OaTable.vue';
import type { SortState } from '../src/components/table-types';
import { relativeTime } from '../src/lib/format';
import { t } from '../src/composables/useI18n';

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

describe('remembered page size', () => {
  afterEach(() => { localStorage.clear(); document.body.textContent = ''; });

  // Asking for a hundred rows used to last until the next reload, then go
  // back to twenty.
  it('keeps the size picked in the pagination across a remount', async () => {
    const rows = Array.from({ length: 150 }, (_, i) => ({ name: `Row ${i}` }));
    const mount = () => {
      const host = document.createElement('div');
      document.body.appendChild(host);
      const app = createApp({ render: () => h(OaTable, {
        columns: [{ key: 'name', header: 'Name', text: (row: { name: string }) => row.name }],
        rows, empty: 'Empty',
      }) });
      app.mount(host);
      return { host, app };
    };

    const first = mount();
    expect(first.host.querySelectorAll('tbody tr')).toHaveLength(20);
    first.host.querySelector<HTMLButtonElement>('.oa-pagination .oa-select')!.click();
    await nextTick(); await nextTick();
    [...document.querySelectorAll<HTMLButtonElement>('[role="option"]')].find((el) => el.textContent?.trim() === '100')!.click();
    await nextTick();
    expect(first.host.querySelectorAll('tbody tr')).toHaveLength(100);
    first.app.unmount();

    const second = mount();
    expect(second.host.querySelectorAll('tbody tr')).toHaveLength(100);
    second.app.unmount();
  });
});

describe('OaTable sort cycling', () => {
  let app: App | null = null;
  let host: HTMLElement;

  beforeEach(() => {
    // A size picked in one test is remembered, and would be the next one's.
    localStorage.clear();
    document.body.textContent = '';
    host = document.createElement('div');
    document.body.appendChild(host);
  });

  afterEach(() => {
    app?.unmount();
    app = null;
    document.body.textContent = '';
  });

  it('reaches rows beyond fifty, changes page size, and recovers after filtering', async () => {
    const rows = ref(Array.from({ length: 65 }, (_, i) => ({ name: `Row ${i + 1}` })));
    app = createApp({ render: () => h(OaTable, {
      columns: [{ key: 'name', header: 'Name', text: (row: { name: string }) => row.name }],
      rows: rows.value, empty: 'Empty',
    }) });
    app.mount(host);
    const texts = () => [...host.querySelectorAll('tbody tr')].map((row) => row.textContent?.trim());
    expect(texts()).toHaveLength(20);
    host.querySelector<HTMLButtonElement>(`button[aria-label="${t('lastPage')}"]`)!.click();
    await nextTick();
    expect(texts()).toEqual(['Row 61', 'Row 62', 'Row 63', 'Row 64', 'Row 65']);
    host.querySelector<HTMLButtonElement>('.oa-pagination .oa-select')!.click();
    await nextTick(); await nextTick();
    const size = [...document.querySelectorAll<HTMLButtonElement>('[role="option"]')].find((el) => el.textContent?.trim() === '50')!;
    size.click(); await nextTick();
    expect(texts()).toHaveLength(50);
    expect(texts()[0]).toBe('Row 1');
    host.querySelector<HTMLButtonElement>(`button[aria-label="${t('lastPage')}"]`)!.click();
    await nextTick();
    expect(texts()).toHaveLength(15);
    rows.value = rows.value.slice(0, 3);
    await nextTick();
    expect(texts()).toEqual(['Row 1', 'Row 2', 'Row 3']);
  });

  it('renders remote pages without slicing them a second time and disables busy navigation', async () => {
    const busy = ref(false);
    const pages: unknown[] = [];
    app = createApp({ render: () => h(OaTable, {
      columns: [{ key: 'name', header: 'Name', text: (row: { name: string }) => row.name }],
      rows: [{ name: 'Row 61' }], empty: 'Empty',
      pagination: { page: 4, pageSize: 20, total: 61 }, busy: busy.value,
      onPage: (page: unknown) => pages.push(page),
    }) });
    app.mount(host);
    expect(host.querySelector('tbody')?.textContent).toContain('Row 61');
    const first = host.querySelector<HTMLButtonElement>(`button[aria-label="${t('firstPage')}"]`)!;
    first.click();
    expect(pages).toEqual([{ page: 1, pageSize: 20 }]);
    busy.value = true; await nextTick();
    expect(first.disabled).toBe(true);
    first.click(); expect(pages).toHaveLength(1);
  });

  it('reorders a later page without dropping the rows on other pages', async () => {
    const rows = Array.from({ length: 25 }, (_, i) => ({ name: String(i) }));
    let reordered: typeof rows = [];
    app = createApp({ render: () => h(OaTable, {
      columns: [{ key: 'name', header: 'Name', text: (row: { name: string }) => row.name }],
      rows, empty: 'Empty', reorderable: true,
      onReorder: (next: unknown[]) => { reordered = next as typeof rows; },
    }) });
    app.mount(host);
    host.querySelector<HTMLButtonElement>(`button[aria-label="${t('lastPage')}"]`)!.click();
    await nextTick();
    const visible = host.querySelectorAll('tbody tr');
    visible[0]!.dispatchEvent(new Event('dragstart'));
    visible[4]!.dispatchEvent(new Event('drop', { cancelable: true }));
    expect(reordered.slice(0, 20)).toEqual(rows.slice(0, 20));
    expect(reordered.slice(20).map((row) => row.name)).toEqual(['21', '22', '23', '24', '20']);
  });

  it('offers no reordering on a server page, where the rows are only part of the list', async () => {
    const rows = Array.from({ length: 5 }, (_, i) => ({ name: String(20 + i) }));
    const reorders: unknown[] = [];
    app = createApp({ render: () => h(OaTable, {
      columns: [{ key: 'name', header: 'Name', text: (row: { name: string }) => row.name }],
      rows, empty: 'Empty', reorderable: true,
      pagination: { page: 2, pageSize: 20, total: 25 },
      onReorder: (next: unknown[]) => reorders.push(next),
    }) });
    app.mount(host);
    const visible = host.querySelectorAll<HTMLTableRowElement>('tbody tr');
    expect(visible).toHaveLength(5);
    expect([...visible].some((row) => row.getAttribute('draggable') === 'true')).toBe(false);
    visible[0]!.dispatchEvent(new Event('dragstart'));
    visible[3]!.dispatchEvent(new Event('drop', { cancelable: true }));
    expect(reorders).toHaveLength(0);
  });

  it('leaves Enter, Space and clicks on a control inside a selectable row to that control', async () => {
    const selected: unknown[] = [];
    app = createApp({ render: () => h(OaTable, {
      columns: [{ key: 'name', header: 'Name' }],
      rows: [{ name: 'Alpha' }], empty: 'Empty', selectable: true,
      onSelect: (row: unknown) => selected.push(row),
    }, {
      'cell-name': () => h('button', { type: 'button' }, 'Copy'),
    }) });
    app.mount(host);
    const button = host.querySelector<HTMLButtonElement>('tbody button')!;
    for (const key of ['Enter', ' ']) {
      const press = new KeyboardEvent('keydown', { key, bubbles: true, cancelable: true });
      button.dispatchEvent(press);
      expect(press.defaultPrevented).toBe(false);
    }
    // A browser turns Enter and Space on a button into a click; jsdom does not,
    // so the click is dispatched here as well.
    button.click();
    expect(selected).toHaveLength(0);

    // The row itself still opens from the keyboard and from the mouse.
    const row = host.querySelector<HTMLTableRowElement>('tbody tr')!;
    row.dispatchEvent(new KeyboardEvent('keydown', { key: 'Enter', bubbles: true, cancelable: true }));
    expect(selected).toHaveLength(1);
    row.click();
    expect(selected).toHaveLength(2);
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
