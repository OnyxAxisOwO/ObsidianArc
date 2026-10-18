import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import { createApp, h, nextTick, ref, shallowRef, type App } from 'vue';
import { createMemoryHistory, createRouter } from 'vue-router';
import { adminApi, type AdminModel, type Announcement } from '../src/admin/api';
import { ApiError } from '../src/api/client';
import OaTable from '../src/components/OaTable.vue';
import { providePanelHost } from '../src/composables/usePanelHost';
import { t } from '../src/composables/useI18n';
import AdminAnnouncements from '../src/views/admin/AdminAnnouncements.vue';
import { pickJSONFile, saveAsFile } from '../src/api/backup';
import AdminModels from '../src/views/admin/AdminModels.vue';
import { provideAdminView } from '../src/views/admin/adminView';

// The import reads a file from the operator's disk; the test hands it a JSON
// body directly. Everything else in the backup module stays real.
vi.mock('../src/api/backup', async (importOriginal) => ({
  ...(await importOriginal<typeof import('../src/api/backup')>()),
  pickJSONFile: vi.fn(),
  saveAsFile: vi.fn(),
}));

const settle = async () => {
  await new Promise((resolve) => setTimeout(resolve, 0));
  await nextTick();
};

describe('OaTable multi-select', () => {
  let app: App | null = null;
  let host: HTMLElement;

  beforeEach(() => {
    host = document.createElement('div');
    document.body.appendChild(host);
  });

  afterEach(() => {
    app?.unmount();
    app = null;
    host.remove();
  });

  const columns = [{ key: 'name', header: 'Name', text: (row: unknown) => (row as { id: string }).id }];

  function mount(initial: Array<{ id: string }>, extra: Record<string, unknown> = {}) {
    const rows = ref(initial);
    const selected = ref<string[]>([]);
    const opened: string[] = [];
    app = createApp({
      render: () => h(OaTable, {
        columns, rows: rows.value, empty: 'Empty', multi: true, selectable: true,
        rowKey: (row: unknown) => (row as { id: string }).id,
        selected: selected.value,
        'onUpdate:selected': (next: string[]) => { selected.value = next; },
        onSelect: (row: unknown) => opened.push((row as { id: string }).id),
        ...extra,
      }),
    });
    app.mount(host);
    return { rows, selected, opened };
  }

  const boxes = () => [...host.querySelectorAll<HTMLInputElement>('tbody input[type="checkbox"]')];

  it('ticks a row without opening it, and the header speaks for the page', async () => {
    const { selected, opened } = mount(Array.from({ length: 25 }, (_, i) => ({ id: `r${i}` })));

    boxes()[1]!.click();
    await nextTick();
    expect(selected.value).toEqual(['r1']);
    expect(opened).toEqual([]);

    const header = host.querySelector<HTMLInputElement>('thead input[type="checkbox"]')!;
    expect(header.indeterminate).toBe(true);
    header.click();
    await nextTick();
    // Twenty to a page, so rows 20-24 are not touched by it.
    expect(selected.value).toHaveLength(20);
    expect(selected.value).not.toContain('r20');
    expect(header.checked).toBe(true);
    expect(host.querySelectorAll('tbody tr.checked')).toHaveLength(20);

    header.click();
    await nextTick();
    expect(selected.value).toEqual([]);
  });

  it('lets go of a row that is no longer in the list', async () => {
    const { rows, selected } = mount([{ id: 'a' }, { id: 'b' }, { id: 'c' }]);
    boxes()[0]!.click();
    await nextTick();
    boxes()[2]!.click();
    await nextTick();
    expect(selected.value).toEqual(['a', 'c']);

    // A filter, or a delete: what is not on screen must not stay selected.
    rows.value = [{ id: 'b' }, { id: 'c' }];
    await nextTick();
    expect(selected.value).toEqual(['c']);
  });

  it('draws no checkboxes unless asked', () => {
    mount([{ id: 'a' }], { multi: false });
    expect(host.querySelector('input[type="checkbox"]')).toBeNull();
  });
});

describe('AdminModels, several at once and in place', () => {
  let app: App | undefined;
  let host: HTMLElement;
  let panels: HTMLElement;
  const reload = vi.fn();

  const model = (id: string, over: Partial<AdminModel> = {}): AdminModel => ({
    id, provider_id: 'p1', provider_name: 'Prov', provider_kind: 'openai',
    model_id: `up-${id}`, api_name: '', system_prompt: '', auto_disabled: false,
    display_name: `Model ${id}`, description: '', avatar: '', enabled: true, hidden: false, sort_order: 0,
    route_to_id: '', reasoning_style: '', reasoning_tiers: [],
    supports_reasoning: false, supports_images: false, supports_vision: false, supports_streaming: true,
    supports_system_prompt: true, supports_tools: false, supports_image_gen: false, supports_chat_image_gen: false,
    emulate_tools: false, context_window: 0, max_output_tokens: 0,
    request_weight: 0, input_token_weight: 1, output_token_weight: 1, reasoning_token_weight: 1,
    ...over,
  });

  let catalogue: AdminModel[];
  let actions: HTMLElement;

  async function mount(models: AdminModel[]) {
    actions = document.createElement('div');
    catalogue = models;
    vi.spyOn(adminApi, 'models').mockImplementation(async () => ({ models: catalogue }));
    vi.spyOn(adminApi, 'providerOptions').mockResolvedValue({ providers: [{ id: 'p1', name: 'Prov', kind: 'openai', enabled: true }] });
    vi.spyOn(adminApi, 'groupOptions').mockResolvedValue({ groups: [] });
    vi.spyOn(adminApi, 'meta').mockResolvedValue({ provider_kinds: ['openai'], reasoning_styles: ['auto'] });
    vi.spyOn(adminApi, 'health').mockResolvedValue({ hours: 24, models: [], policy: { probe: true, window_mins: 30, disable_after: 0 } });

    const router = createRouter({ history: createMemoryHistory(), routes: [{ path: '/:pathMatch(.*)*', component: { render: () => null } }] });
    app = createApp({
      setup() {
        providePanelHost(shallowRef(panels));
        provideAdminView({ actionsHost: actions, setTitle() {}, reload, params: [] });
        return () => h(AdminModels);
      },
    });
    app.use(router);
    app.mount(host);
    await settle();
  }

  const rowBox = (index: number) => host.querySelectorAll<HTMLInputElement>('tbody input[type="checkbox"]')[index]!;
  const barButton = (label: string) => [...host.querySelectorAll<HTMLButtonElement>('.oa-bulk-bar button')]
    .find((button) => button.textContent?.trim() === label)!;

  beforeEach(() => {
    host = document.createElement('div');
    panels = document.createElement('div');
    document.body.append(host, panels);
    reload.mockClear();
  });

  afterEach(() => {
    app?.unmount();
    app = undefined;
    host.remove();
    panels.remove();
    vi.restoreAllMocks();
  });

  it('shows no bar until a row is ticked, then disables only the ones that are on', async () => {
    const update = vi.spyOn(adminApi, 'updateModel').mockResolvedValue({ model: model('a') });
    await mount([model('a'), model('b', { enabled: false }), model('c')]);
    expect(host.querySelector('.oa-bulk-bar')).toBeNull();

    rowBox(0).click();
    await nextTick();
    rowBox(1).click();
    await nextTick();
    expect(host.querySelector('.oa-bulk-bar')!.textContent).toContain(t('bulkSelected', { count: 2 }));

    barButton(t('bulkDisable')).click();
    await settle();
    // `b` was already off, so it costs no request.
    expect(update).toHaveBeenCalledTimes(1);
    expect(update).toHaveBeenCalledWith('a', { enabled: false });
    expect(adminApi.models).toHaveBeenCalledTimes(2);
    expect(host.querySelector('.oa-bulk-bar')).toBeNull();
  });

  it('keeps what failed ticked and says why', async () => {
    vi.spyOn(adminApi, 'updateModel').mockImplementation(async (id) => {
      if (id === 'b') throw new ApiError(409, 'conflict', 'Another model routes to this one.');
      return { model: model(id) };
    });
    await mount([model('a'), model('b')]);
    rowBox(0).click();
    await nextTick();
    rowBox(1).click();
    await nextTick();

    barButton(t('bulkDisable')).click();
    await settle();

    const bar = host.querySelector('.oa-bulk-bar')!;
    expect(bar.textContent).toContain(t('bulkPartial', { failed: 1, total: 2, reason: 'Another model routes to this one.' }));
    expect(bar.textContent).toContain(t('bulkSelected', { count: 1 }));
    expect(rowBox(1).checked).toBe(true);
  });

  it('deletes only on the second click', async () => {
    const remove = vi.spyOn(adminApi, 'deleteModel').mockImplementation(async (id) => {
      catalogue = catalogue.filter((entry) => entry.id !== id);
    });
    await mount([model('a'), model('b'), model('c')]);
    rowBox(0).click();
    await nextTick();
    rowBox(2).click();
    await nextTick();

    barButton(t('bulkDelete')).click();
    await nextTick();
    expect(remove).not.toHaveBeenCalled();

    // Armed, the button reads as the question.
    const armed = host.querySelector<HTMLButtonElement>('.oa-bulk-bar .oa-btn.armed')!;
    expect(armed.textContent).toContain(t('bulkDeleteConfirm', { count: 2 }));
    armed.click();
    await settle();
    expect(remove.mock.calls.map(([id]) => id)).toEqual(['a', 'c']);
    expect(host.querySelectorAll('tbody tr')).toHaveLength(1);
    expect(host.querySelector('.oa-bulk-bar')).toBeNull();
  });

  // The complaint this exists for: saving an edit remounted the page, which
  // threw the reader back to the first page of rows and the top of the body.
  it('saves an edit without remounting the page or leaving the page of rows being read', async () => {
    vi.spyOn(adminApi, 'updateModel').mockResolvedValue({ model: model('a') });
    await mount(Array.from({ length: 25 }, (_, i) => model(String(i).padStart(2, '0'))));

    const table = host.querySelector('table')!;
    host.querySelector<HTMLButtonElement>(`button[aria-label="${t('lastPage')}"]`)!.click();
    await nextTick();
    expect(host.querySelectorAll('tbody tr')).toHaveLength(5);

    host.querySelector<HTMLTableRowElement>('tbody tr')!.click();
    await settle();
    panels.querySelector<HTMLButtonElement>('.oa-panel-foot .oa-btn.primary')!.click();
    await settle();

    expect(adminApi.updateModel).toHaveBeenCalledTimes(1);
    expect(adminApi.models).toHaveBeenCalledTimes(2);
    expect(reload).not.toHaveBeenCalled();
    expect(host.querySelector('table')).toBe(table);
    expect(host.querySelectorAll('tbody tr')).toHaveLength(5);
    expect(host.querySelector('tbody tr')!.textContent).toContain('Model 20');

    // It slides out, and only then is it gone.
    expect(panels.querySelector('.oa-panel')).not.toBeNull();
    await new Promise((resolve) => setTimeout(resolve, 400));
    expect(panels.querySelector('.oa-panel')).toBeNull();
  });

  it('adds every ticked model with the form\'s settings, and keeps the ones that failed ticked', async () => {
    vi.spyOn(adminApi, 'detect').mockResolvedValue({ models: [
      { model_id: 'gpt-6-sol', display_name: '', configured: false },
      { model_id: 'gpt-6-astra', display_name: 'Astra', configured: false },
      { model_id: 'gpt-5.5', display_name: '', configured: false },
    ] });
    const create = vi.spyOn(adminApi, 'createModel').mockImplementation(async (body) => {
      if (body['model_id'] === 'gpt-5.5') throw new ApiError(400, 'bad', 'no');
      return { model: model('n') };
    });
    await mount([model('a')]);

    [...actions.querySelectorAll<HTMLButtonElement>('button')].find((button) => button.textContent?.trim() === t('addModel'))!.click();
    await settle();
    panels.querySelector<HTMLButtonElement>('.oa-detect-run')!.click();
    await settle();
    const rows = () => [...panels.querySelectorAll<HTMLButtonElement>('.oa-detect-option')];
    rows()[0]!.click();
    await nextTick();
    // One tick fills the fields, as picking one always did.
    expect(panels.querySelector<HTMLInputElement>('.oa-field input')!.value).toBe('gpt-6-sol');
    rows()[1]!.click();
    await nextTick();
    rows()[2]!.click();
    await nextTick();

    const confirm = panels.querySelector<HTMLButtonElement>('.oa-panel-foot .oa-btn.primary')!;
    expect(confirm.textContent?.trim()).toBe(t('addPickedN', { count: 3 }));
    confirm.click();
    await settle();

    expect(create.mock.calls.map(([body]) => [body['model_id'], body['display_name'], body['api_name'], body['provider_id']])).toEqual([
      ['gpt-6-sol', 'gpt-6-sol', '', 'p1'],
      ['gpt-6-astra', 'Astra', '', 'p1'],
      ['gpt-5.5', 'gpt-5.5', '', 'p1'],
    ]);
    expect(create.mock.calls[0]![0]['supports_streaming']).toBe(true);
    expect(panels.querySelector('.oa-drawer-flash')?.textContent).toContain('gpt-5.5');
    expect(rows().filter((row) => row.classList.contains('picked')).map((row) => row.textContent?.trim())).toEqual(['gpt-5.5']);
  });

  // Opening a stored free image model used to reset its weight to 1, because
  // the seeding watched the flag that open() also sets. Saving then kept it.
  it('keeps a stored free image model at weight zero when it is opened and saved', async () => {
    const update = vi.spyOn(adminApi, 'updateModel').mockResolvedValue({ model: model('img') });
    await mount([model('img', { supports_image_gen: true, request_weight: 0 })]);

    host.querySelector<HTMLTableRowElement>('tbody tr')!.click();
    await settle();
    panels.querySelector<HTMLButtonElement>('.oa-panel-foot .oa-btn.primary')!.click();
    await settle();

    expect(update).toHaveBeenCalledWith('img', expect.objectContaining({ request_weight: 0 }));
  });

  it('seeds the weight to one when the operator ticks image generation on', async () => {
    const update = vi.spyOn(adminApi, 'updateModel').mockResolvedValue({ model: model('txt') });
    await mount([model('txt', { request_weight: 0 })]);

    host.querySelector<HTMLTableRowElement>('tbody tr')!.click();
    await settle();
    const toggle = [...panels.querySelectorAll<HTMLLabelElement>('.oa-checkbox-field')]
      .find((field) => field.querySelector('span')?.textContent === t('capImageGen'))!
      .querySelector<HTMLInputElement>('input[type="checkbox"]')!;
    toggle.click();
    await settle();
    panels.querySelector<HTMLButtonElement>('.oa-panel-foot .oa-btn.primary')!.click();
    await settle();

    expect(update).toHaveBeenCalledWith('txt', expect.objectContaining({ request_weight: 1, supports_image_gen: true }));
  });

  // navigator.clipboard is absent on a plain-http origin; the shared helper
  // falls back to execCommand, and the button must say the copy worked.
  it('copies a model ID through the fallback where the clipboard API is missing', async () => {
    const execCommand = vi.fn(() => true);
    Object.defineProperty(navigator, 'clipboard', { value: undefined, configurable: true });
    Object.defineProperty(document, 'execCommand', { value: execCommand, configurable: true });
    try {
      await mount([model('a')]);
      host.querySelector<HTMLTableRowElement>('tbody tr')!.click();
      await settle();
      const copy = panels.querySelector<HTMLButtonElement>(`button[aria-label="${t('copy')}"]`)!;
      copy.click();
      await settle();

      expect(execCommand).toHaveBeenCalledWith('copy');
      expect(panels.querySelector<HTMLButtonElement>(`button[aria-label="${t('copied')}"]`)).not.toBeNull();
    } finally {
      Reflect.deleteProperty(navigator, 'clipboard');
      Reflect.deleteProperty(document, 'execCommand');
    }
  });

  it('says why a refused move was refused, and still snaps back to the stored order', async () => {
    vi.spyOn(adminApi, 'reorderModels').mockRejectedValue(new ApiError(409, 'order_conflict', 'Another administrator moved this model first.'));
    await mount([model('a'), model('b')]);

    const rows = host.querySelectorAll('tbody tr');
    rows[0]!.dispatchEvent(new Event('dragstart'));
    rows[1]!.dispatchEvent(new Event('drop', { cancelable: true }));
    await settle();

    expect(adminApi.reorderModels).toHaveBeenCalledWith(['b', 'a']);
    expect(host.querySelector('[role="alert"]')?.textContent).toContain('Another administrator moved this model first.');
    expect(adminApi.models).toHaveBeenCalledTimes(2);
  });
});

describe('AdminModels, what the filter hides', () => {
  let app: App | undefined;
  let host: HTMLElement;
  let panels: HTMLElement;
  let actions: HTMLElement;

  const model = (id: string): AdminModel => ({
    id, provider_id: 'p1', provider_name: 'Prov', provider_kind: 'openai',
    model_id: `up-${id}`, api_name: '', system_prompt: '', auto_disabled: false,
    display_name: `Model ${id}`, description: '', avatar: '', enabled: true, hidden: false, sort_order: 0,
    route_to_id: '', reasoning_style: '', reasoning_tiers: [],
    supports_reasoning: false, supports_images: false, supports_vision: false, supports_streaming: true,
    supports_system_prompt: true, supports_tools: false, supports_image_gen: false, supports_chat_image_gen: false,
    emulate_tools: false, context_window: 0, max_output_tokens: 0,
    request_weight: 0, input_token_weight: 1, output_token_weight: 1, reasoning_token_weight: 1,
  });

  async function mount(models: AdminModel[]): Promise<void> {
    actions = document.createElement('div');
    vi.spyOn(adminApi, 'models').mockResolvedValue({ models });
    vi.spyOn(adminApi, 'providerOptions').mockResolvedValue({ providers: [{ id: 'p1', name: 'Prov', kind: 'openai', enabled: true }] });
    vi.spyOn(adminApi, 'groupOptions').mockResolvedValue({ groups: [] });
    vi.spyOn(adminApi, 'meta').mockResolvedValue({ provider_kinds: ['openai'], reasoning_styles: ['auto'] });
    vi.spyOn(adminApi, 'health').mockResolvedValue({ hours: 24, models: [], policy: { probe: true, window_mins: 30, disable_after: 0 } });
    const router = createRouter({ history: createMemoryHistory(), routes: [{ path: '/:pathMatch(.*)*', component: { render: () => null } }] });
    app = createApp({
      setup() {
        providePanelHost(shallowRef(panels));
        provideAdminView({ actionsHost: actions, setTitle() {}, reload() {}, params: [] });
        return () => h(AdminModels);
      },
    });
    app.use(router);
    app.mount(host);
    await settle();
  }

  const rowBox = (index: number) => host.querySelectorAll<HTMLInputElement>('tbody input[type="checkbox"]')[index]!;
  const search = () => host.querySelector<HTMLInputElement>('input[type="search"]')!;
  const typeSearch = async (value: string) => {
    search().value = value;
    search().dispatchEvent(new Event('input', { bubbles: true }));
    await settle();
  };

  beforeEach(() => {
    host = document.createElement('div');
    panels = document.createElement('div');
    document.body.append(host, panels);
  });

  afterEach(async () => {
    // The filter is kept for the page's lifetime, so it must not leak into the next test.
    if (search()) await typeSearch('');
    app?.unmount();
    app = undefined;
    host.remove();
    panels.remove();
    vi.restoreAllMocks();
  });

  it('counts and acts on the ticked rows it shows, and lets go of the ones it hides', async () => {
    const update = vi.spyOn(adminApi, 'updateModel').mockResolvedValue({ model: model('a') });
    await mount([model('a'), model('b'), model('c')]);
    rowBox(0).click();
    await nextTick();
    rowBox(1).click();
    await nextTick();
    rowBox(2).click();
    await nextTick();
    expect(host.querySelector('.oa-bulk-bar')!.textContent).toContain(t('bulkSelected', { count: 3 }));

    await typeSearch('Model a');
    // b and c are still ticked but off screen: the bar must not count them, or
    // the confirm would name rows the action never touches.
    expect(host.querySelector('.oa-bulk-bar')!.textContent).toContain(t('bulkSelected', { count: 1 }));

    const disable = [...host.querySelectorAll<HTMLButtonElement>('.oa-bulk-bar button')]
      .find((button) => button.textContent?.trim() === t('bulkDisable'))!;
    disable.click();
    await settle();
    expect(update).toHaveBeenCalledTimes(1);
    expect(update).toHaveBeenCalledWith('a', { enabled: false });

    await typeSearch('');
    expect(rowBox(1).checked).toBe(false);
    expect(rowBox(2).checked).toBe(false);
  });

  // The report lists what an import would not take, keyed by the line. Two
  // refusals with the same text (or a later report that repeats one) gave
  // Vue duplicate keys to patch between.
  it('lists skipped lines that repeat without a duplicate-key clash', async () => {
    const warn = vi.spyOn(console, 'warn').mockImplementation(() => {});
    await mount([model('a')]);
    vi.mocked(pickJSONFile).mockResolvedValue({ models: [] });
    const importButton = () => [...host.querySelectorAll<HTMLButtonElement>('button'), ...actions.querySelectorAll<HTMLButtonElement>('button')]
      .find((button) => button.textContent?.trim() === t('importModels'))!;
    const importing = (skipped: string[]) => vi.spyOn(adminApi, 'importModels')
      .mockResolvedValueOnce({ created: 0, updated: 0, skipped } as unknown as Awaited<ReturnType<typeof adminApi.importModels>>);

    importing(['a', 'b']);
    importButton().click();
    await settle();
    await settle();
    expect(panels.querySelectorAll('.oa-code-line')).toHaveLength(2);

    importing(['c', 'd', 'd']);
    importButton().click();
    await settle();
    await settle();
    expect(panels.querySelectorAll('.oa-code-line')).toHaveLength(3);
    expect(warn.mock.calls.some((args) => String(args[0]).includes('Duplicate keys'))).toBe(false);
  });

  // The export is what an import reads back, field for field. The request
  // override was added to the form and the save but not to the export, so a
  // catalogue moved between instances arrived without its overrides.
  it('exports the request override so an import gives it back', async () => {
    await mount([{ ...model('a'), request_override: '{"temperature":0.2}' }]);
    [...actions.querySelectorAll<HTMLButtonElement>('button')]
      .find((button) => button.textContent?.trim() === t('exportModels'))!.click();

    const [, contents] = vi.mocked(saveAsFile).mock.lastCall!;
    const exported = JSON.parse(contents) as { models: Array<Record<string, unknown>> };
    expect(exported.models[0]!['request_override']).toBe('{"temperature":0.2}');
  });
});

describe('AdminAnnouncements, publishing several at once', () => {
  let app: App | undefined;
  let host: HTMLElement;

  afterEach(() => {
    app?.unmount();
    app = undefined;
    host.remove();
    vi.restoreAllMocks();
  });

  // The announcement update replaces the whole record, so a bulk publish that
  // sent only `published` would blank the title and the body of every row.
  it('sends each announcement back whole with only its published flag changed', async () => {
    const draft: Announcement = {
      id: 'n1', title: 'Maintenance', body: 'Tonight.', display_mode: 'always',
      dismiss_after_seconds: 30, published: false, pinned: true, created_at: 1, updated_at: 1, read: false,
    };
    vi.spyOn(adminApi, 'announcements').mockResolvedValue({ announcements: [draft, { ...draft, id: 'n2', published: true }] });
    const update = vi.spyOn(adminApi, 'updateAnnouncement').mockResolvedValue({ announcement: draft });

    host = document.createElement('div');
    document.body.appendChild(host);
    const router = createRouter({ history: createMemoryHistory(), routes: [{ path: '/:pathMatch(.*)*', component: { render: () => null } }] });
    app = createApp({
      setup() {
        provideAdminView({ actionsHost: document.createElement('div'), setTitle() {}, reload() {}, params: [] });
        return () => h(AdminAnnouncements);
      },
    });
    app.use(router);
    app.mount(host);
    await settle();

    const boxes = host.querySelectorAll<HTMLInputElement>('tbody input[type="checkbox"]');
    boxes[0]!.click();
    await nextTick();
    boxes[1]!.click();
    await nextTick();
    [...host.querySelectorAll<HTMLButtonElement>('.oa-bulk-bar button')]
      .find((button) => button.textContent?.trim() === t('bulkPublish'))!.click();
    await settle();

    // The second was already published.
    expect(update).toHaveBeenCalledTimes(1);
    expect(update).toHaveBeenCalledWith('n1', {
      title: 'Maintenance', body: 'Tonight.', display_mode: 'always',
      dismiss_after_seconds: 30, published: true, pinned: true,
    });
  });
});
