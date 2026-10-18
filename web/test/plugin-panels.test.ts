// What a plugin can open: a panel of its own for everybody signed in, and a
// record of one of its backoffice lists. Held here: the core draws the
// controls the plugin declared and hands back exactly what was entered; a
// required field stops the send; the reader's own records are listed; a row
// opens only when the plugin says rows open, shows its fields — a link only
// when it is http(s) — and an action reads the table again; and a page with
// two lists has one record open, not one per list.

import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import { h, nextTick, type Component } from 'vue';
import { changeLanguage, t } from '../src/composables/useI18n';
import { installPlugins, userPanels } from '../src/plugins/registry';
import type { AdminListSpec, ArcPlugin, RecordDetail, UserFormValues, UserPanelSpec } from '../src/plugins/types';
import PluginUserPanel from '../src/views/PluginUserPanel.vue';
import { provideOpenRecord } from '../src/views/admin/openRecord';
import PluginList from '../src/views/admin/PluginList.vue';
import PluginActionCard from '../src/views/admin/PluginActionCard.vue';
import { safeModeCategories, safeModeEnabled } from '../src/admin/safeMode';
import { catchAllRouter, adminShell, mountComponent, PAST_PANEL_ZOOM_MS, settle, wait, button } from './helpers';

const deferred = <T,>() => {
  let resolve!: (value: T) => void;
  const promise = new Promise<T>((done) => { resolve = done; });
  return { promise, resolve };
};

let unmount: (() => void) | undefined;
let host: HTMLElement;
let panelHost: HTMLElement;

async function mount(component: Component, props: Record<string, unknown> = {}): Promise<void> {
  unmount = await mountComponent(component, props, {
    host, panels: panelHost, adminView: adminShell(), router: await catchAllRouter('/'),
  });
}

function input(root: ParentNode, label: string): HTMLInputElement | HTMLTextAreaElement {
  const node = [...root.querySelectorAll<HTMLElement>('.oa-field')]
    .find((candidate) => candidate.querySelector('.oa-field-label')?.textContent?.trim() === label);
  const found = node?.querySelector<HTMLInputElement | HTMLTextAreaElement>('input, textarea');
  if (!found) throw new Error(`Missing field: ${label}`);
  return found;
}

function type(field: HTMLInputElement | HTMLTextAreaElement, value: string): void {
  field.value = value;
  field.dispatchEvent(new Event('input', { bubbles: true }));
}

beforeEach(async () => {
  await changeLanguage('en');
  host = document.createElement('div');
  panelHost = document.createElement('div');
  document.body.append(host, panelHost);
});

afterEach(() => {
  unmount?.();
  unmount = undefined;
  host.remove();
  panelHost.remove();
  installPlugins([]);
  safeModeEnabled.value = false;
  vi.restoreAllMocks();
});

function panel(run: (values: UserFormValues) => Promise<string>): UserPanelSpec {
  return {
    slug: 'tips',
    title: () => 'Tips',
    intro: () => 'Tell us something.',
    controls: [
      { kind: 'choice', key: 'kind', label: () => 'Kind', options: [
        { value: 'a', label: () => 'Alpha' }, { value: 'b', label: () => 'Beta' },
      ] },
      { kind: 'text', key: 'link', label: () => 'Link', required: true },
      { kind: 'textarea', key: 'body', label: () => 'Details' },
      { kind: 'switch', key: 'quiet', label: () => 'Quietly' },
      { kind: 'images', key: 'shots', label: () => 'Pictures', max: 2 },
    ],
    submit: { label: () => 'Send tip', run },
    mine: {
      title: () => 'Yours',
      empty: () => 'Nothing yet',
      load: async () => [{ id: '1', title: 'Earlier tip', badge: { label: 'Accepted', tone: 'default' }, note: 'Thanks!' }],
    },
  };
}

describe('a plugin\'s panel for everybody signed in', () => {
  it('is offered by the registry only while its plugin is loaded', () => {
    expect(userPanels()).toEqual([]);
    const spec = panel(async () => '');
    installPlugins([{ name: 'tipper', userPanels: [spec] } satisfies ArcPlugin]);
    expect(userPanels().map((entry) => entry.slug)).toEqual(['tips']);
  });

  it('draws the declared controls and sends what was entered', async () => {
    const run = vi.fn(async () => 'Got it');
    installPlugins([{ name: 'tipper', userPanels: [panel(run)] }]);
    await mount(PluginUserPanel, { slug: 'tips' });

    expect(panelHost.textContent).toContain('Tell us something.');
    expect(panelHost.textContent).toContain('Earlier tip');
    expect(panelHost.textContent).toContain('Thanks!');

    button(panelHost, 'Beta').click();
    type(input(panelHost, 'Link'), 'https://example.com/repo');
    type(input(panelHost, 'Details'), 'It calls the API.');
    await nextTick();
    button(panelHost, 'Send tip').click();
    await settle();

    expect(run).toHaveBeenCalledWith({
      kind: 'b', link: 'https://example.com/repo', body: 'It calls the API.', quiet: false, shots: [],
    });
    expect(panelHost.textContent).toContain('Got it');
    // Cleared for the next one.
    expect((input(panelHost, 'Link') as HTMLInputElement).value).toBe('');
  });

  it('does not send without a required field', async () => {
    const run = vi.fn(async () => '');
    installPlugins([{ name: 'tipper', userPanels: [panel(run)] }]);
    await mount(PluginUserPanel, { slug: 'tips' });
    button(panelHost, 'Send tip').click();
    await settle();
    expect(run).not.toHaveBeenCalled();
    expect(panelHost.textContent).toContain(t('pluginFieldRequired', { field: 'Link' }));
  });

  it('lets a list grow and shrink row by row, and sends only the rows filled in', async () => {
    const run = vi.fn(async () => 'Got it');
    installPlugins([{ name: 'tipper', userPanels: [{
      slug: 'tips',
      title: () => 'Tips',
      controls: [{ kind: 'list', key: 'who', label: () => 'Accounts', add: () => 'Add account', max: 3, required: true }],
      submit: { label: () => 'Send tip', run },
    }] }]);
    await mount(PluginUserPanel, { slug: 'tips' });
    const rows = () => [...panelHost.querySelectorAll<HTMLInputElement>('.oa-plugin-list-row input')];

    // One row to start, and nothing to remove while it is the only one.
    expect(rows()).toHaveLength(1);
    expect(panelHost.querySelector('.oa-plugin-list-row .oa-icon-btn')).toBeNull();
    button(panelHost, 'Send tip').click();
    await settle();
    expect(run).not.toHaveBeenCalled();

    type(rows()[0]!, ' alice ');
    await nextTick();
    button(panelHost, 'Add account').click();
    await nextTick();
    button(panelHost, 'Add account').click();
    await nextTick();
    // Three is the most: the button goes.
    expect(rows()).toHaveLength(3);
    expect([...panelHost.querySelectorAll('button')].some((node) => node.textContent?.trim() === 'Add account')).toBe(false);
    type(rows()[1]!, 'bob');
    type(rows()[2]!, 'carol');
    await nextTick();
    panelHost.querySelectorAll<HTMLButtonElement>('.oa-plugin-list-row .oa-icon-btn')[1]!.click();
    await nextTick();
    expect(rows().map((row) => row.value)).toEqual([' alice ', 'carol']);

    button(panelHost, 'Add account').click();
    await nextTick();
    button(panelHost, 'Send tip').click();
    await settle();
    expect(run).toHaveBeenCalledWith({ who: ['alice', 'carol'] });
  });

  it('says so when no loaded plugin has the panel', async () => {
    // The plugins have answered by the time a panel is drawn: boot waits for them.
    installPlugins([]);
    await mount(PluginUserPanel, { slug: 'nowhere' });
    expect(panelHost.textContent).toContain(t('pluginPanelMissing'));
    // Once answered, the title must not stay on "Loading" over the missing notice.
    expect(panelHost.textContent).not.toContain(t('loading'));
  });
});

describe('a record of a plugin\'s list, opened', () => {
  function list(detail?: AdminListSpec['detail']): { spec: AdminListSpec; load: ReturnType<typeof vi.fn> } {
    const load = vi.fn(async () => ({ rows: [{ id: 'r1', title: 'Leak' }], total: 1 }));
    return {
      load,
      spec: {
        id: 'reports', page: 'plugin:tips', title: () => 'Reports', empty: () => 'None',
        columns: [{ key: 'title', header: () => 'Title' }],
        load, cell: (key, row) => ({ title: String(row[key]) }),
        ...(detail ? { detail } : {}),
      },
    };
  }

  it('opens nothing when the plugin did not make rows open', async () => {
    const { spec } = list();
    await mount(PluginList, { spec });
    host.querySelector<HTMLElement>('tbody tr')!.click();
    await settle();
    expect(panelHost.querySelector('.oa-plugin-record')).toBeNull();
  });

  it('shows the fields, links only http(s), and reads the table again after an action', async () => {
    const run = vi.fn(async (draft: Record<string, string>) => `Rewarded ${draft.cards}`);
    const detail: RecordDetail = {
      title: 'Leak report',
      fields: [
        { label: 'Where', value: 'https://forum.example/thread', link: true },
        { label: 'Other', value: 'javascript:alert(1)', link: true },
      ],
      actions: [{
        id: 'accept', label: 'Accept',
        controls: [{ kind: 'text', key: 'cards', label: () => 'Cards' }],
        defaults: { cards: '1' },
        run,
      }],
    };
    const { spec, load } = list(async () => detail);
    await mount(PluginList, { spec });
    host.querySelector<HTMLElement>('tbody tr')!.click();
    await settle();

    const links = [...panelHost.querySelectorAll<HTMLAnchorElement>('.oa-plugin-record a')];
    expect(links.map((link) => link.getAttribute('href'))).toEqual(['https://forum.example/thread']);
    expect(panelHost.textContent).toContain('javascript:alert(1)');

    type(input(panelHost, 'Cards'), '3');
    await nextTick();
    button(panelHost, 'Accept').click();
    await settle();
    expect(run).toHaveBeenCalledWith({ cards: '3' });
    expect(panelHost.textContent).toContain('Rewarded 3');
    expect(load).toHaveBeenCalledTimes(2);
  });

  // Two 520px panels side by side left the tables and the page heading one
  // character wide, which is how a plugin's pending and decided requests
  // were read.
  it('keeps one record open on a page with two lists', async () => {
    const pending = list(async () => ({ title: 'Pending one', fields: [] })).spec;
    const decided = { ...list(async () => ({ title: 'Decided one', fields: [] })).spec, id: 'decided' };
    await mount({ setup() {
      provideOpenRecord();
      return () => [h(PluginList, { spec: pending }), h(PluginList, { spec: decided })];
    } });
    const rows = () => [...host.querySelectorAll<HTMLElement>('tbody tr')];
    const titles = () => [...panelHost.querySelectorAll('.oa-plugin-record')].length;

    rows()[0]!.click();
    await settle();
    expect(titles()).toBe(1);
    expect(panelHost.textContent).toContain('Pending one');

    rows()[1]!.click();
    await settle();
    expect(titles()).toBe(1);
    expect(panelHost.textContent).toContain('Decided one');
    expect(panelHost.textContent).not.toContain('Pending one');

    rows()[0]!.click();
    await settle();
    expect(titles()).toBe(1);
    expect(panelHost.textContent).toContain('Pending one');
  });

  // Rows need not carry an id, so the panel cannot be told apart by one. A
  // second record must start with its own drafts, not the first one's typing.
  it('starts a second record with its own drafts when the rows carry no id', async () => {
    const spec: AdminListSpec = {
      id: 'reports', page: 'plugin:tips', title: () => 'Reports', empty: () => 'None',
      columns: [{ key: 'title', header: () => 'Title' }],
      load: async () => ({ rows: [{ title: 'First' }, { title: 'Second' }], total: 2 }),
      cell: (key, row) => ({ title: String(row[key]) }),
      detail: async (row) => ({
        title: String(row.title),
        fields: [],
        actions: [{
          id: 'grant', label: 'Grant',
          controls: [{ kind: 'text', key: 'cards', label: () => 'Cards' }],
          defaults: { cards: '1' },
          run: async () => 'Granted',
        }],
      }),
    };
    await mount(PluginList, { spec });
    host.querySelectorAll<HTMLElement>('tbody tr')[0]!.click();
    await settle();
    type(input(panelHost, 'Cards'), '3');
    await nextTick();

    host.querySelectorAll<HTMLElement>('tbody tr')[1]!.click();
    await settle();
    expect(panelHost.textContent).toContain('Second');
    expect((input(panelHost, 'Cards') as HTMLInputElement).value).toBe('1');
  });

  // The table and its page controls are the only way to ask for another page,
  // so a page that failed must leave them on screen, with a retry that asks for
  // that page again.
  it('keeps the table and its pages under a failed page, and retries that page', async () => {
    const load = vi.fn()
      .mockResolvedValueOnce({ rows: [{ id: 'r1', title: 'Leak' }], total: 500 })
      .mockRejectedValueOnce(new Error('upstream down'))
      .mockResolvedValueOnce({ rows: [{ id: 'r2', title: 'Second page' }], total: 500 });
    const spec: AdminListSpec = {
      id: 'reports', page: 'plugin:tips', title: () => 'Reports', empty: () => 'None',
      columns: [{ key: 'title', header: () => 'Title' }],
      load, cell: (key, row) => ({ title: String(row[key]) }),
    };
    await mount(PluginList, { spec });
    button(host, t('next')).click();
    await settle();

    expect(host.textContent).toContain('upstream down');
    expect(host.querySelector('tbody')!.textContent).toContain('Leak');
    expect(host.querySelector('nav.oa-pagination')).not.toBeNull();

    button(host, t('tryAgain')).click();
    await settle();
    expect(load).toHaveBeenCalledTimes(3);
    expect(host.textContent).not.toContain('upstream down');
    expect(host.querySelector('tbody')!.textContent).toContain('Second page');
  });

  it('draws a destructive action as a destructive button', async () => {
    const detail: RecordDetail = {
      title: 'Leak report',
      fields: [],
      actions: [{ id: 'revoke', label: 'Revoke access', danger: true, run: async () => 'Revoked' }],
    };
    const { spec } = list(async () => detail);
    await mount(PluginList, { spec });
    host.querySelector<HTMLElement>('tbody tr')!.click();
    await settle();

    expect(button(panelHost, 'Revoke access').classList.contains('oa-btn-danger')).toBe(true);
  });

  // The address is hidden on screen, so its link must not carry it either: a
  // hover or a click would show the address that the mask is there to hide.
  it('does not link a masked address, even when it is http(s)', async () => {
    safeModeEnabled.value = true;
    safeModeCategories.value.users = true;
    const detail: RecordDetail = {
      title: 'Leak report',
      fields: [{ label: 'Where', value: 'https://forum.example/thread', link: true, mask: true }],
    };
    const { spec } = list(async () => detail);
    await mount(PluginList, { spec });
    host.querySelector<HTMLElement>('tbody tr')!.click();
    await settle();

    expect(panelHost.querySelector('.oa-plugin-record a')).toBeNull();
    expect(panelHost.querySelector('.oa-plugin-record')!.innerHTML).not.toContain('forum.example');
  });
});

describe('a plugin\'s action card', () => {
  it('draws a destructive button as one', async () => {
    const card = {
      id: 'revoke-all', page: 'plugin:tips' as const, title: () => 'Revoke', controls: [],
      button: { label: () => 'Revoke everything', danger: true, run: async () => 'Revoked' },
    };
    await mount(PluginActionCard, { card });

    expect(button(host, 'Revoke everything').classList.contains('oa-btn-danger')).toBe(true);
  });

  // A plugin that wrote no question for its destructive button used to get a
  // one-click action: the core drew the danger but asked nothing in place.
  it('asks before a destructive button runs when the plugin wrote no question', async () => {
    const run = vi.fn(async () => 'Revoked');
    const card = {
      id: 'revoke-all', page: 'plugin:tips' as const, title: () => 'Revoke', controls: [],
      button: { label: () => 'Revoke everything', danger: true, run },
    };
    await mount(PluginActionCard, { card });

    button(host, 'Revoke everything').click();
    await settle();
    expect(run).not.toHaveBeenCalled();
    button(host, 'Revoke everything').click();
    await settle();
    expect(run).toHaveBeenCalledTimes(1);
  });
});

describe('a plugin\'s list read again while an older read is in flight', () => {
  const rowsSpec = (over: Partial<AdminListSpec>): AdminListSpec => ({
    id: 'reports', page: 'plugin:tips', title: () => 'Reports', empty: () => 'None',
    columns: [{ key: 'title', header: () => 'Title' }],
    load: async () => ({ rows: [], total: 0 }),
    cell: (key, row) => ({ title: String(row[key]) }),
    ...over,
  });

  // A page change does not wait for the page before it, so the older answer can
  // arrive last. The reader asked for the newer page, and must keep seeing it.
  it('shows the page asked for last, not the page whose answer came back last', async () => {
    const second = deferred<{ rows: Array<Record<string, unknown>>; total: number }>();
    const spec = rowsSpec({
      load: vi.fn(async (offset: number) => {
        if (offset === 0) return { rows: [{ id: 'p1', title: 'Page one' }], total: 60 };
        if (offset === 20) return second.promise;
        return { rows: [{ id: 'p3', title: 'Page three' }], total: 60 };
      }),
    });
    await mount(PluginList, { spec });

    button(host, t('next')).click();
    await settle();
    button(host, t('next')).click();
    await settle();
    expect(host.textContent).toContain('Page three');

    second.resolve({ rows: [{ id: 'p2', title: 'Page two' }], total: 60 });
    await settle();
    expect(host.textContent).toContain('Page three');
    expect(host.textContent).not.toContain('Page two');
  });

  // Two rows opened in turn: the record of the row clicked last is the one shown.
  it('shows the record of the row opened last, not the one whose detail came back last', async () => {
    const first = deferred<RecordDetail>();
    const spec = rowsSpec({
      load: async () => ({ rows: [{ id: 'r1', title: 'First row' }, { id: 'r2', title: 'Second row' }], total: 2 }),
      detail: (row) => (row.id === 'r1' ? first.promise : Promise.resolve({ title: 'Second record', fields: [] })),
    });
    await mount(PluginList, { spec });
    const rows = () => [...host.querySelectorAll<HTMLElement>('tbody tr')];

    rows()[0]!.click();
    await nextTick();
    rows()[1]!.click();
    await settle();
    first.resolve({ title: 'First record', fields: [] });
    await settle();

    expect(panelHost.textContent).toContain('Second record');
    expect(panelHost.textContent).not.toContain('First record');
  });

  // An action reads the table again. A row opened while that read is in flight
  // is the one the panel shows, so the refresh of the record opened before it
  // must not be written over the newer one when it lands.
  it('keeps the record opened during an action\'s table read, not the refresh of the one before it', async () => {
    const reload = deferred<{ rows: Array<Record<string, unknown>>; total: number }>();
    const bDetail = deferred<RecordDetail>();
    const refreshA = deferred<RecordDetail>();
    const rowsNow = { rows: [{ id: 'r1', title: 'Row A' }, { id: 'r2', title: 'Row B' }], total: 2 };
    let loads = 0;
    let aReads = 0;
    const recordA: RecordDetail = {
      title: 'Record A',
      fields: [],
      actions: [{ id: 'mark', label: 'Mark', controls: [], defaults: {}, run: async () => 'Marked' }],
    };
    const spec = rowsSpec({
      load: async () => {
        loads += 1;
        return loads === 2 ? reload.promise : rowsNow;
      },
      detail: (row) => {
        if (row.id === 'r2') return bDetail.promise;
        aReads += 1;
        return aReads === 1 ? Promise.resolve(recordA) : refreshA.promise;
      },
    });
    await mount(PluginList, { spec });
    const rows = () => [...host.querySelectorAll<HTMLElement>('tbody tr')];

    rows()[0]!.click();
    await settle();
    button(panelHost, 'Mark').click();
    await settle();
    rows()[1]!.click();
    await settle();
    reload.resolve(rowsNow);
    await settle();
    bDetail.resolve({ title: 'Record B', fields: [] });
    await settle();
    refreshA.resolve({ title: 'Record A again', fields: [] });
    await settle();

    expect(panelHost.textContent).toContain('Record B');
    expect(panelHost.textContent).not.toContain('Record A again');
  });
});

describe('a record whose detail is still loading when the reader moves on', () => {
  const oneRowList = (id: string, title: string, detail: NonNullable<AdminListSpec['detail']>): AdminListSpec => ({
    id, page: 'plugin:tips', title: () => id, empty: () => 'None',
    columns: [{ key: 'title', header: () => 'Title' }],
    load: async () => ({ rows: [{ id: `${id}-row`, title }], total: 1 }),
    cell: (key, row) => ({ title: String(row[key]) }),
    detail,
  });

  // The reader dismissed the panel while B was loading. B must stay away.
  it('does not show the record once the reader has closed the panel', async () => {
    const detailB = deferred<RecordDetail>();
    const spec: AdminListSpec = {
      id: 'reports', page: 'plugin:tips', title: () => 'Reports', empty: () => 'None',
      columns: [{ key: 'title', header: () => 'Title' }],
      load: async () => ({ rows: [{ id: 'a', title: 'Row A' }, { id: 'b', title: 'Row B' }], total: 2 }),
      cell: (key, row) => ({ title: String(row[key]) }),
      detail: async (row) => (row.id === 'a' ? { title: 'Record A', fields: [] } : detailB.promise),
    };
    await mount(PluginList, { spec });
    const rows = () => [...host.querySelectorAll<HTMLElement>('tbody tr')];

    rows()[0]!.click();
    await settle();
    expect(panelHost.textContent).toContain('Record A');

    rows()[1]!.click();
    await nextTick();
    panelHost.querySelector<HTMLButtonElement>(`button[aria-label="${t('close')}"]`)!.click();
    // The panel closes after its 340ms exit, so the close lands only once that
    // has run; B's detail must arrive after it, with the reader already gone.
    await wait(PAST_PANEL_ZOOM_MS);
    expect(panelHost.querySelector('.oa-plugin-record')).toBeNull();

    detailB.resolve({ title: 'Record B', fields: [] });
    await settle();
    expect(panelHost.textContent).not.toContain('Record B');
  });

  // Another list took the one shared panel after this list's open began. This
  // list's older answer must not put its record back over that one.
  it('does not let a record still loading in one list replace one another list opened later', async () => {
    const pendingX = deferred<RecordDetail>();
    const pending = oneRowList('pending', 'Pending row', async () => pendingX.promise);
    const decided = oneRowList('decided', 'Decided row', async () => ({ title: 'Decided record', fields: [] }));
    await mount({ setup() {
      provideOpenRecord();
      return () => [h(PluginList, { spec: pending }), h(PluginList, { spec: decided })];
    } });
    const rows = () => [...host.querySelectorAll<HTMLElement>('tbody tr')];

    rows()[0]!.click();
    await nextTick();
    rows()[1]!.click();
    await settle();
    expect(panelHost.textContent).toContain('Decided record');

    pendingX.resolve({ title: 'Pending record', fields: [] });
    await settle();
    expect(panelHost.textContent).toContain('Decided record');
    expect(panelHost.textContent).not.toContain('Pending record');
    expect(panelHost.querySelectorAll('.oa-plugin-record')).toHaveLength(1);
  });
});
