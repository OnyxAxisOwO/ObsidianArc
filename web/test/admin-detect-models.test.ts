import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import { createApp, h, nextTick, type App } from 'vue';
import { adminApi } from '../src/admin/api';
import AdminDetectModels, { type DetectedModel } from '../src/views/admin/AdminDetectModels.vue';

const settle = async () => {
  await new Promise((resolve) => setTimeout(resolve, 0));
  await nextTick();
};

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
    app = createApp({ render: () => h(AdminDetectModels, { providerId: 'p1', mode: 'add', ...props } as never) });
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

  it('hands one row back in the model editor and folds the list away', async () => {
    const picked: DetectedModel[] = [];
    mount({ mode: 'pick', onPick: (entry: DetectedModel) => picked.push(entry) });
    await detect();

    options()[2]!.click();
    await nextTick();
    expect(picked.map((entry) => entry.model_id)).toEqual(['qwen3.6-27b']);
    expect(options()).toHaveLength(0);
  });
});
