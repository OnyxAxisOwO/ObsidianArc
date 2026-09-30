// The bonus bars on the usage panel. What matters here is what is *said*: a
// bar whose total the administrator keeps private must not leak it into the
// page, and the line under each bar must tell an account whether that bar is
// being spent before its allowance or after — the same credits mean something
// different in each.
//
// The API is mocked at the client module, so the component's own fetch paths
// and the URL it builds for a switch are the real code under test.

import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import { createApp, defineComponent, h, nextTick, ref, type App } from 'vue';
import { api } from '@/api/client';
import type { BonusBar } from '@/api/bonus';
import OaBonusBars from '@/components/OaBonusBars.vue';
import { changeLanguage, t } from '@/composables/useI18n';

vi.mock('@/api/client', () => ({
  ApiError: class ApiError extends Error {
    readonly status: number;
    readonly code: string;
    constructor(status: number, code: string, message: string) {
      super(message);
      this.name = 'ApiError';
      this.status = status;
      this.code = code;
    }
  },
  api: { get: vi.fn(), put: vi.fn() },
}));

const get = vi.mocked(api.get);
const put = vi.mocked(api.put);

let app: App | null = null;
let host: HTMLElement;

beforeEach(async () => {
  await changeLanguage('en');
  host = document.createElement('div');
  document.body.appendChild(host);
});

afterEach(() => {
  app?.unmount();
  app = null;
  document.body.textContent = '';
  vi.clearAllMocks();
});

function bar(over: Partial<BonusBar> = {}): BonusBar {
  return {
    bar_id: 'bar-1', name: 'Launch gift', description: '', kind: 'bonus', toggle_mode: 'user',
    choosable: true, enabled: true, show_total: true, total: 1000, remaining: 640, percent: 64,
    expires_at: 0, model_ids: [], exhausted: false,
    ...over,
  };
}

async function settle(): Promise<void> {
  await new Promise((resolve) => setTimeout(resolve, 0));
  await nextTick();
}

/** Mounts the bars under a host that can bump the refresh key, as the panel does. */
async function mount(): Promise<{ refresh: () => Promise<void> }> {
  const key = ref(0);
  app = createApp(defineComponent({ render: () => h(OaBonusBars, { refreshKey: key.value }) }));
  app.mount(host);
  await settle();
  return { refresh: async () => { key.value += 1; await settle(); } };
}

function serve(bars: BonusBar[]): void {
  get.mockImplementation(async (url: string) => {
    if (url === '/api/bonus') return { bars };
    throw new Error(`unexpected ${url}`);
  });
}

describe('OaBonusBars', () => {
  it('draws nothing for an account that holds no bonus, and no heading either', async () => {
    serve([]);
    await mount();
    expect(host.querySelector('.oa-bonus')).toBeNull();
    expect(host.textContent).toBe('');
  });

  it('shows the amounts of a bar whose total is public', async () => {
    serve([bar()]);
    await mount();
    expect(host.textContent).toContain(t('bonusLeft', { left: '640', total: '1000' }));
  });

  it('shows only a percentage when the administrator keeps the total private', async () => {
    // The server sends nulls; the point is that nothing on the page derives
    // the figure from anything else, such as the percentage.
    serve([bar({ show_total: false, total: null, remaining: null, percent: 37 })]);
    await mount();
    expect(host.textContent).toContain(t('bonusPercentLeft', { percent: 37 }));
    expect(host.textContent).not.toContain('1000');
    expect(host.textContent).not.toContain('640');
  });

  it('says a bar is used up rather than showing an empty figure', async () => {
    serve([bar({ exhausted: true, remaining: 0, percent: 0 })]);
    await mount();
    expect(host.textContent).toContain(t('bonusExhausted'));
    expect(host.querySelector('.oa-bonus-bar')?.classList.contains('is-spent')).toBe(true);
  });

  it('says whether a bar is spent before the allowance or after it', async () => {
    serve([
      bar({ bar_id: 'on', name: 'On', enabled: true }),
      bar({ bar_id: 'off', name: 'Off', enabled: false }),
      bar({ bar_id: 'spare', name: 'Spare', kind: 'reserve', enabled: false, choosable: false }),
    ]);
    await mount();
    const lines = [...host.querySelectorAll('.oa-bonus-bar')].map((node) => node.textContent ?? '');
    expect(lines[0]).toContain(t('bonusBeforeAllowance'));
    expect(lines[1]).toContain(t('bonusAfterAllowance'));
    // A reserve is last whatever it says about itself, and is labelled so.
    expect(lines[2]).toContain(t('bonusAfterAllowance'));
    expect(lines[2]).toContain(t('bonusKindReserve'));
  });

  it('names when a bar expires and how many models it covers', async () => {
    serve([
      bar({ bar_id: 'a', name: 'A', model_ids: ['m1'], expires_at: Date.UTC(2030, 0, 15, 12) }),
      bar({ bar_id: 'b', name: 'B', model_ids: ['m1', 'm2', 'm3'] }),
    ]);
    await mount();
    const [first, second] = [...host.querySelectorAll('.oa-bonus-bar')].map((node) => node.textContent ?? '');
    expect(first).toContain(t('bonusModelsOne'));
    expect(first).toMatch(/Expires/);
    expect(second).toContain(t('bonusModelsOther', { count: 3 }));
    expect(second).toContain(t('bonusNoExpiry'));
  });

  it('shows the switch only on a bar the account may choose', async () => {
    serve([
      bar({ bar_id: 'free', name: 'Free' }),
      bar({ bar_id: 'forced', name: 'Forced', toggle_mode: 'on', choosable: false }),
    ]);
    await mount();
    const rows = [...host.querySelectorAll('.oa-bonus-bar')];
    expect(rows[0]!.querySelector('input[type="checkbox"]')).not.toBeNull();
    expect(rows[1]!.querySelector('input[type="checkbox"]')).toBeNull();
  });

  it('sends the switch to the bar it belongs to, and keeps the answer', async () => {
    serve([bar({ bar_id: 'a/b', enabled: true })]);
    put.mockResolvedValue(undefined);
    await mount();

    host.querySelector<HTMLInputElement>('input[type="checkbox"]')!.click();
    await settle();

    // The id is encoded: an operator-visible id is never trusted into a path.
    expect(put).toHaveBeenCalledWith('/api/bonus/a%2Fb/choice', { enabled: false });
    expect(host.querySelector('.oa-bonus-bar')!.textContent).toContain(t('bonusAfterAllowance'));
  });

  it('keeps the switch where it was and says why when the server refuses', async () => {
    serve([bar({ enabled: true })]);
    const { ApiError } = await import('@/api/client');
    put.mockRejectedValue(new ApiError(409, 'not_choosable', 'That bar is fixed.'));
    await mount();

    host.querySelector<HTMLInputElement>('input[type="checkbox"]')!.click();
    await settle();

    expect(host.textContent).toContain('That bar is fixed.');
    expect(host.querySelector<HTMLInputElement>('input[type="checkbox"]')!.checked).toBe(true);
  });

  it('shows what the description says as text, never as markup', async () => {
    serve([bar({ description: '<img src=x onerror=alert(1)>' })]);
    await mount();
    expect(host.querySelector('img')).toBeNull();
    expect(host.textContent).toContain('<img src=x onerror=alert(1)>');
  });

  it('reads again when the host asks, and keeps what it has when that read fails', async () => {
    serve([bar({ remaining: 640 })]);
    const { refresh } = await mount();
    expect(host.textContent).toContain('640');

    serve([bar({ remaining: 500, percent: 50 })]);
    await refresh();
    expect(host.textContent).toContain('500');

    get.mockRejectedValue(new Error('offline'));
    await refresh();
    // A refresh the account did not ask for does not trade figures for an error.
    expect(host.textContent).toContain('500');
    expect(host.textContent).not.toContain(t('bonusLoadFailed'));
  });

  it('says so when the first read fails', async () => {
    get.mockRejectedValue(new Error('offline'));
    await mount();
    expect(host.textContent).toContain(t('bonusLoadFailed'));
  });
});
