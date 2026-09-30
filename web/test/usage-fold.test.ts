// The usage screen's sections fold. The panel is long — the allowance, bonus
// bars, check-in, reset cards, the totals and the turns — and what somebody
// opened it for is usually one of them. What is held here is the wiring: every
// section has a heading that folds it, the controls that live in a heading still
// work while it is folded, and a folded section is still what the reader left.
//
// The API is mocked at the client module, so the panel's own sections are the
// real code under test.

import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import { createApp, h, nextTick, ref, type App } from 'vue';
import { api } from '@/api/client';
import { providePanelHost } from '@/composables/usePanelHost';
import { changeLanguage, t } from '@/composables/useI18n';
import UsagePanel from '@/views/UsagePanel.vue';

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
  api: {
    get: vi.fn() as unknown as <T>(url: string) => Promise<T>,
    post: vi.fn() as unknown as <T>(url: string, body?: unknown) => Promise<T>,
    put: vi.fn() as unknown as <T>(url: string, body?: unknown) => Promise<T>,
  },
}));

vi.mock('vue-router', () => ({ useRouter: () => ({ push: vi.fn() }) }));

const get = vi.mocked(api.get);

let app: App | null = null;
let host: HTMLElement;

beforeEach(async () => {
  await changeLanguage('en');
  localStorage.clear();
  vi.useFakeTimers();
  host = document.createElement('div');
  document.body.appendChild(host);
  get.mockImplementation(async (url: string) => {
    if (url === '/api/usage/me') return { unlimited: false, display: 'remaining', windows: [] };
    if (url === '/api/usage/me/history') {
      return { totals: { requests: 0, input_tokens: 0, output_tokens: 0, reasoning_tokens: 0, total_tokens: 0, credits: 0, errors: 0 }, turns: [] };
    }
    if (url === '/api/usage/cards') return { cards: [] };
    if (url === '/api/bonus') {
      return { display: 'remaining', bars: [{
        bar_id: 'b1', name: 'Launch gift', description: '', kind: 'bonus', toggle_mode: 'user', choosable: true, enabled: true,
        show_total: true, total: 10, remaining: 6, percent: 60, expires_at: 0, model_ids: [], exhausted: false,
      }] };
    }
    if (url === '/api/checkin') {
      return { enabled: true, today: '2030-02-10', checked_in_today: false, streak: 2, month_days: [9], month_count: 1, daily: { kind: '' }, rules: [] };
    }
    throw new Error(`unexpected ${url}`);
  });
});

afterEach(() => {
  app?.unmount();
  app = null;
  document.body.textContent = '';
  vi.clearAllMocks();
  vi.useRealTimers();
});

async function settle(): Promise<void> {
  await vi.advanceTimersByTimeAsync(0);
  await nextTick();
}

async function mountPanel(): Promise<void> {
  const panelHost = document.createElement('div');
  document.body.appendChild(panelHost);
  app = createApp({
    setup() {
      providePanelHost(ref(panelHost));
    },
    render: () => h(UsagePanel),
  });
  app.mount(host);
  await settle();
}

function toggles(): HTMLButtonElement[] {
  return [...document.querySelectorAll<HTMLButtonElement>('.oa-collapsible-toggle')];
}

function toggleFor(title: string): HTMLButtonElement {
  const found = toggles().find((node) => node.textContent?.trim() === title);
  if (!found) throw new Error(`No section called ${title}; there are ${toggles().map((node) => node.textContent?.trim()).join(', ')}`);
  return found;
}

const expanded = (title: string): string | null => toggleFor(title).getAttribute('aria-expanded');

describe('the usage panel’s sections', () => {
  it('has a heading that folds for each of its sections, all open to begin with', async () => {
    await mountPanel();
    const titles = toggles().map((node) => node.textContent?.trim());
    expect(titles).toEqual([
      t('secAllowance'), t('secBonus'), t('secCheckin'), t('secCards'), t('secTotals'), t('secRecentTurns'),
    ]);
    for (const title of titles) expect(expanded(title!)).toBe('true');
  });

  it('folds one section without touching the others, and remembers it for the next visit', async () => {
    await mountPanel();
    toggleFor(t('secTotals')).click();
    await settle();

    expect(expanded(t('secTotals'))).toBe('false');
    expect(expanded(t('secRecentTurns'))).toBe('true');
    expect(localStorage.getItem('obsidian-arc-fold-usage-totals')).toBe('1');

    app?.unmount();
    app = null;
    document.body.textContent = '';
    host = document.createElement('div');
    document.body.appendChild(host);
    await mountPanel();
    expect(expanded(t('secTotals'))).toBe('false');
    expect(expanded(t('secAllowance'))).toBe('true');
  });

  it('still refreshes from the allowance heading while that section is folded', async () => {
    await mountPanel();
    toggleFor(t('secAllowance')).click();
    await settle();
    get.mockClear();

    const refresh = [...document.querySelectorAll<HTMLButtonElement>('button')].find((node) => node.getAttribute('aria-label') === t('refresh'))!;
    refresh.click();
    await settle();

    expect(get.mock.calls.map(([url]) => url)).toContain('/api/usage/me');
    expect(expanded(t('secAllowance'))).toBe('false');
  });

  it('opens the reset cards section for the code field the plus asks for, without changing what was kept', async () => {
    await mountPanel();
    toggleFor(t('secCards')).click();
    await settle();
    expect(expanded(t('secCards'))).toBe('false');

    const plus = [...document.querySelectorAll<HTMLButtonElement>('button')].find((node) => node.getAttribute('aria-label') === t('redeemAdd'))!;
    plus.click();
    await settle();

    expect(expanded(t('secCards'))).toBe('true');
    expect(document.querySelector<HTMLElement>('.oa-redeem-row')!.hidden).toBe(false);
    expect(localStorage.getItem('obsidian-arc-fold-usage-cards')).toBe('1');
  });

  it('keeps the check-in button and the streak in view while the section is folded', async () => {
    await mountPanel();
    toggleFor(t('secCheckin')).click();
    await settle();

    const head = toggleFor(t('secCheckin')).closest('.oa-collapsible-head')!;
    expect(head.textContent).toContain(t('checkinStreak', { count: 2 }));
    expect([...head.querySelectorAll('button')].some((node) => node.textContent?.trim() === t('checkinNow'))).toBe(true);
  });
});
