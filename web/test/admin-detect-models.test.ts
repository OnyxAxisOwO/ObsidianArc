import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import { createApp, h, nextTick, reactive, type App } from 'vue';
import { adminApi } from '../src/admin/api';
import { ApiError } from '../src/api/client';
import { t } from '../src/composables/useI18n';
import AdminDetectModels, { type DetectedModel } from '../src/views/admin/AdminDetectModels.vue';
import { settle } from './helpers';

;

const catalogue = () => ({
  models: [
    { model_id: 'gpt-5.5', display_name: '', configured: false },
    { model_id: 'claude-opus-5-5', display_name: 'Claude Opus 5.5', configured: true },
    { model_id: 'qwen3.6-27b', display_name: '', configured: false },
    { model_id: 'qwen3.6-235b', display_name: '', configured: false },
  ],
});

describe('detected models', () => {
  let app: App | null = null;
  let host: HTMLElement;

  beforeEach(() => {
    host = document.createElement('div');
    document.body.appendChild(host);
    vi.spyOn(adminApi, 'detect').mockImplementation(async () => catalogue());
  });

  afterEach(() => {
    app?.unmount();
    app = null;
    host.remove();
    vi.restoreAllMocks();
  });

  function mount(props: Record<string, unknown>) {
    app = createApp({ render: () => h(AdminDetectModels, { providerId: 'p1', mode: 'add', ...props }) });
    app.mount(host);
  }

  const options = () => [...host.querySelectorAll<HTMLButtonElement>('.oa-detect-option')];
  const ids = () => options().map((row) => row.querySelector('.oa-detect-option-id')!.textContent!.trim());

  async function detect(): Promise<void> {
    host.querySelector<HTMLButtonElement>('.oa-detect-run')!.click();
    await settle();
  }

  async function search(text: string): Promise<void> {
    const input = host.querySelector<HTMLInputElement>('.oa-search input')!;
    input.value = text;
    input.dispatchEvent(new Event('input'));
    await nextTick();
  }

  it('shows nothing but its row until asked, then the list and a search over it', async () => {
    mount({ mode: 'add', canAdd: true });
    expect(options()).toHaveLength(0);
    expect(host.querySelector('.oa-search')).toBeNull();

    await detect();
    expect(adminApi.detect).toHaveBeenCalledWith('p1');
    expect(ids()).toHaveLength(4);

    await search('QWEN');
    expect(ids()).toEqual(['qwen3.6-27b', 'qwen3.6-235b']);
    await search('zzz');
    expect(options()).toHaveLength(0);
    expect(host.querySelector('.oa-detect-none')).not.toBeNull();
  });

  it('adds the ticked rows together, and an added row cannot be ticked again', async () => {
    const created = vi.spyOn(adminApi, 'createModel').mockResolvedValue({} as never);
    mount({ mode: 'add', canAdd: true });
    await detect();

    expect(options()[1]!.disabled).toBe(true);
    options()[0]!.click();
    options()[2]!.click();
    await nextTick();
    const commit = [...host.querySelectorAll<HTMLButtonElement>('.oa-btn.primary')].at(-1)!;
    commit.click();
    await settle();

    expect(created.mock.calls.map(([body]) => body['model_id'])).toEqual(['gpt-5.5', 'qwen3.6-27b']);
    expect(options()[0]!.disabled).toBe(true);
    expect(options()[2]!.disabled).toBe(true);
    // Nothing ticked any more, so nothing to commit.
    expect(host.querySelector('.oa-btn.primary')).toBeNull();
  });

  it('keeps only the rows that failed ticked, so a retry does not create the ones that worked', async () => {
    const created = vi.spyOn(adminApi, 'createModel')
      .mockResolvedValueOnce({} as never)
      .mockRejectedValueOnce(new ApiError(500, 'internal', 'boom'));
    mount({ mode: 'add', canAdd: true });
    await detect();

    options()[0]!.click();
    options()[2]!.click();
    await nextTick();
    [...host.querySelectorAll<HTMLButtonElement>('.oa-btn.primary')].at(-1)!.click();
    await settle();

    expect(options()[0]!.querySelector('.oa-detect-known')).not.toBeNull();
    expect(options()[2]!.getAttribute('aria-selected')).toBe('true');
    // Each refusal names its model, so the row that did not make it can be
    // found. A plain Error takes the String(error) branch and would show
    // "Error: boom", which no real request produces.
    expect(host.querySelector('.oa-group-row-meta')?.textContent).toBe('qwen3.6-27b: boom');
    expect(host.textContent).not.toContain('Error: boom');

    created.mockClear();
    // Once the two queued answers are used, spyOn falls back to the real
    // request, so the retry gets its own answer here.
    created.mockResolvedValue({} as never);
    [...host.querySelectorAll<HTMLButtonElement>('.oa-btn.primary')].at(-1)!.click();
    await settle();
    expect(created.mock.calls.map(([body]) => body['model_id'])).toEqual(['qwen3.6-27b']);
  });

  it('names every row that failed with its reason, and translates a code the page knows', async () => {
    vi.spyOn(adminApi, 'createModel')
      .mockRejectedValueOnce(new ApiError(409, 'model_routed', 'Another model routes to this one.'))
      .mockRejectedValueOnce(new ApiError(500, 'internal', 'boom'));
    mount({ mode: 'add', canAdd: true });
    await detect();

    options()[0]!.click();
    options()[2]!.click();
    await nextTick();
    [...host.querySelectorAll<HTMLButtonElement>('.oa-btn.primary')].at(-1)!.click();
    await settle();

    // A code with a reader's sentence shows that sentence; one without keeps the
    // server's, so an administrator still sees what the request said.
    expect(host.querySelector('.oa-group-row-meta')?.textContent)
      .toBe(`gpt-5.5: ${t('errModelRouted')}; qwen3.6-27b: boom`);
  });

  it('drops a detection that comes back for a provider the editor has since left', async () => {
    let release: () => void = () => {};
    vi.spyOn(adminApi, 'detect').mockImplementation(() => new Promise((resolve) => {
      release = () => resolve(catalogue());
    }));
    const state = reactive({ providerId: 'p1' });
    app = createApp({
      render: () => h(AdminDetectModels, { providerId: state.providerId, mode: 'add', canAdd: true }),
    });
    app.mount(host);

    await detect();
    state.providerId = 'p2';
    await settle();
    release();
    await settle();

    expect(options()).toHaveLength(0);
    expect(host.querySelector('.oa-detect-options')).toBeNull();
  });

  it('ticks several rows in the model editor, the configured ones too, and hands them all back', async () => {
    let picked: DetectedModel[] = [];
    mount({ mode: 'pick', 'onUpdate:picked': (entries: DetectedModel[]) => { picked = entries; } });
    await detect();

    options()[2]!.click();
    await nextTick();
    options()[1]!.click();
    await nextTick();
    expect(picked.map((entry) => entry.model_id)).toEqual(['qwen3.6-27b', 'claude-opus-5-5']);
    expect(options()).toHaveLength(4);

    options()[2]!.click();
    await nextTick();
    expect(picked.map((entry) => entry.model_id)).toEqual(['claude-opus-5-5']);
  });
});
