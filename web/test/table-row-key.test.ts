// A row is keyed by its rowKey wherever one is given, so whatever a cell holds
// travels with its row. A column's width is only known in px, so the width a
// table needs before it scrolls is worked out from px widths alone.

import { afterEach, describe, expect, it } from 'vitest';
import { createApp, h, nextTick, ref, type App, type Ref } from 'vue';
import OaTable from '../src/components/OaTable.vue';
import type { Column } from '../src/components/table-types';

interface Item {
  id: string;
  name: string;
}

let app: App | undefined;

afterEach(() => {
  app?.unmount();
  app = undefined;
});

function mount(rows: Ref<Item[]>, columns: Column<Item>[], extra: Record<string, unknown> = {}): HTMLElement {
  const host = document.createElement('div');
  document.body.appendChild(host);
  app = createApp({
    render: () => h(OaTable, {
      rows: rows.value,
      columns,
      empty: '',
      rowKey: (row: unknown) => (row as Item).id,
      ...extra,
    }, {
      'cell-name': ({ row }: { row: unknown }) => h('input', { 'data-row': (row as Item).id }),
    }),
  });
  app.mount(host);
  return host;
}

describe('OaTable row identity', () => {
  it('moves a cell control with its row when the rows are reordered', async () => {
    const rows = ref<Item[]>([{ id: 'a', name: 'Alpha' }, { id: 'b', name: 'Beta' }]);
    const host = mount(rows, [{ key: 'name', header: 'Name' }]);
    const inputFor = (position: number): HTMLInputElement =>
      host.querySelectorAll('tbody tr')[position]!.querySelector('input')!;

    inputFor(0).value = 'typed for alpha';
    rows.value = [rows.value[1]!, rows.value[0]!];
    await nextTick();

    expect(inputFor(1).dataset['row']).toBe('a');
    expect(inputFor(1).value).toBe('typed for alpha');
  });
});

describe('OaTable scroll width', () => {
  it('counts a rem or percent column at the default width, and a px column at its own', () => {
    const host = mount(ref<Item[]>([{ id: 'a', name: 'Alpha' }]), [
      { key: 'name', header: 'Name', width: '12rem' },
      { key: 'id', header: 'Id', width: '200px' },
    ]);

    const block = host.querySelector<HTMLElement>('.oa-table')!.parentElement!;
    expect(block.style.minWidth).toBe(`${140 + 200}px`);
  });

  it('adds the checkbox column when the table can select rows', () => {
    const host = mount(ref<Item[]>([{ id: 'a', name: 'Alpha' }]), [
      { key: 'name', header: 'Name', width: '100px' },
    ], { multi: true, selected: [] });

    const block = host.querySelector<HTMLElement>('.oa-table')!.parentElement!;
    expect(block.style.minWidth).toBe(`${40 + 100}px`);
  });
});
