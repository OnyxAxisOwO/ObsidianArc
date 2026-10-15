// The instance's default allowance. When the stored policies cannot be read,
// the panel must not open on a blank form: Save would write nulls over the
// instance-wide caps that every account falls back to. A list that really has
// no global policy yet still opens blank, as it always did.

import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import { createApp, h, nextTick, shallowRef, type App } from 'vue';
import { adminApi } from '../src/admin/api';
import { providePanelHost } from '../src/composables/usePanelHost';
import { changeLanguage, t } from '../src/composables/useI18n';
import AdminUsage from '../src/views/admin/AdminUsage.vue';
import { provideAdminView } from '../src/views/admin/adminView';

vi.mock('@/api/client', () => ({
  ApiError: class ApiError extends Error {
    readonly status: number;
    readonly code: string;
    readonly details: Record<string, unknown>;
    constructor(status: number, code: string, message: string, details: Record<string, unknown> = {}) {
      super(message);
      this.name = 'ApiError';
      this.status = status;
      this.code = code;
      this.details = details;
    }
  },
  api: {
    get: vi.fn() as unknown as <T>(url: string) => Promise<T>,
    post: vi.fn() as unknown as <T>(url: string, body?: unknown) => Promise<T>,
    put: vi.fn() as unknown as <T>(url: string, body?: unknown) => Promise<T>,
  },
}));

const settle = async (): Promise<void> => {
  await new Promise((resolve) => setTimeout(resolve, 0));
  await nextTick();
};

const usagePayload = {
  totals: { requests: 3, input_tokens: 0, output_tokens: 0, reasoning_tokens: 0, total_tokens: 0, credits: 0, errors: 0 },
  by_model: [], by_provider: [], by_user: [], series: [], bucket_ms: 3600000,
};

let app: App | undefined;
let host: HTMLElement;
let actions: HTMLElement;
let panels: HTMLElement;

function mount(policies: () => Promise<unknown>, reload: () => void = () => {}): void {
  vi.spyOn(adminApi, 'policies').mockImplementation(policies as typeof adminApi.policies);
  const savePolicy = vi.spyOn(adminApi, 'savePolicy').mockResolvedValue({ policy: {} as never });
  vi.spyOn(adminApi, 'usage').mockResolvedValue(usagePayload as never);
  vi.spyOn(adminApi, 'usageRecords').mockResolvedValue({ records: [], total: 0 } as never);
  vi.spyOn(adminApi, 'groupOptions').mockResolvedValue({ groups: [] } as never);
  // The policy form's credit field prices models with this when it opens.
  vi.spyOn(adminApi, 'models').mockResolvedValue({ models: [] } as never);
  app = createApp({
    setup() {
      providePanelHost(shallowRef(panels));
      provideAdminView({ actionsHost: actions, setTitle() {}, reload, params: [] });
      return () => h(AdminUsage);
    },
  });
  app.mount(host);
  expect(savePolicy).not.toHaveBeenCalled();
}

function openDefaults(): void {
  const button = actions.querySelector<HTMLButtonElement>('#defaultLimits');
  if (!button) throw new Error('Missing default limits button');
  button.click();
}

beforeEach(async () => {
  await changeLanguage('en');
  host = document.createElement('div');
  actions = document.createElement('div');
  panels = document.createElement('div');
  document.body.append(host, actions, panels);
});

afterEach(() => {
  app?.unmount();
  app = undefined;
  document.body.textContent = '';
  vi.restoreAllMocks();
});

describe('AdminUsage default allowance', () => {
  it('does not open a blank form when the stored policies cannot be read', async () => {
    mount(async () => { throw new Error('policy service unavailable'); });
    await settle();

    openDefaults();
    await settle();

    expect(document.body.textContent).not.toContain(t('adminsExemptHint'));
    expect(document.body.textContent).toContain('policy service unavailable');
    expect(adminApi.savePolicy).not.toHaveBeenCalled();
  });

  // The page refreshes itself every fifteen seconds and clears its general
  // error after each good answer. A failed read of the defaults is not a
  // general error: it must still be there when the refresh has come back.
  it('keeps a failed read of the defaults on screen through the refresh', async () => {
    vi.useFakeTimers();
    try {
      mount(async () => { throw new Error('policy service unavailable'); });
      await vi.advanceTimersByTimeAsync(0);
      await nextTick();

      openDefaults();
      await vi.advanceTimersByTimeAsync(0);
      await nextTick();
      expect(document.body.textContent).toContain('policy service unavailable');

      // The initial load also calls usage, so the refresh is only proven by a
      // call made after this point.
      const before = vi.mocked(adminApi.usage).mock.calls.length;
      await vi.advanceTimersByTimeAsync(15_000);
      await nextTick();
      expect(document.body.textContent).toContain('policy service unavailable');
      expect(vi.mocked(adminApi.usage).mock.calls.length).toBeGreaterThan(before);
    } finally {
      vi.useRealTimers();
    }
  });

  // Saving the defaults used to reload the whole page, which dropped the
  // drill-down the operator was reading. The figures refresh in place instead.
  it('keeps the drill-down when the defaults are saved', async () => {
    const modelId = '01ARZ3NDEKTSV4RRFFQ69G5FAV';
    window.history.replaceState(null, '', `/admin/usage?model=${modelId}`);
    const reload = vi.fn();
    try {
      mount(async () => ({ policies: [] }), reload);
      await settle();

      openDefaults();
      await settle();
      const save = [...panels.querySelectorAll<HTMLButtonElement>('button')].find((button) => button.textContent?.trim() === t('save'));
      expect(save).toBeDefined();
      save!.click();
      await settle();

      expect(adminApi.savePolicy).toHaveBeenCalledTimes(1);
      expect(reload).not.toHaveBeenCalled();
      const lastUsage = vi.mocked(adminApi.usage).mock.calls.at(-1)?.[0] ?? '';
      expect(lastUsage).toContain(`model_id=${modelId}`);
    } finally {
      window.history.replaceState(null, '', '/');
    }
  });

  it('still opens blank when the list really has no global policy yet', async () => {
    mount(async () => ({ policies: [] }));
    await settle();

    openDefaults();
    await settle();

    expect(document.body.textContent).toContain(t('adminsExemptHint'));
    expect(document.body.textContent).not.toContain('policy service unavailable');
  });
});
