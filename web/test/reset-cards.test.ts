// Reset cards on the usage panel: how a pile of them is drawn, and what the
// refresh button refreshes.
//
// Both behaviours come from the same complaint. Somebody holding a dozen
// cards was reading a dozen near-identical rows to work out how many they
// had, and somebody who pressed refresh saw the bars move while the totals
// underneath stayed where they were — which reads as a button that does
// nothing.
//
// The API is mocked at the client module, so the panel's own fetch paths,
// grouping and click handlers are the real code under test.

import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import { createApp, h, nextTick, ref, type App } from 'vue';
import { api } from '@/api/client';
import { providePanelHost } from '@/composables/usePanelHost';
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
  },
}));

vi.mock('vue-router', () => ({ useRouter: () => ({ push: vi.fn() }) }));

const get = vi.mocked(api.get);
const post = vi.mocked(api.post);

let app: App | null = null;
let host: HTMLElement;

beforeEach(() => {
  vi.useFakeTimers();
  // Pinned to one in the morning, local time. Grouping is by calendar day, so
  // a suite that ran in the evening would push a "nine hours from now" card
  // past midnight and into a day of its own.
  vi.setSystemTime(new Date(2026, 4, 1, 1, 0, 0));
  host = document.createElement('div');
  document.body.appendChild(host);
});

afterEach(() => {
  app?.unmount();
  app = null;
  document.body.textContent = '';
  vi.clearAllMocks();
  vi.useRealTimers();
});

function mountPanel(): void {
  const panelHost = document.createElement('div');
  document.body.appendChild(panelHost);
  app = createApp({
    setup() {
      providePanelHost(ref(panelHost));
    },
    render: () => h(UsagePanel),
  });
  app.mount(host);
}

async function settle(): Promise<void> {
  await vi.advanceTimersByTimeAsync(0);
  await nextTick();
}

const emptyTotals = {
  requests: 0,
  input_tokens: 0,
  output_tokens: 0,
  reasoning_tokens: 0,
  total_tokens: 0,
  credits: 0,
  errors: 0,
};

/** One card, `hours` from now, with an id derived from that offset. */
function card(hours: number, name?: string, windows?: string[]) {
  return {
    id: `card-${hours}`,
    name,
    windows,
    source: 'grant' as const,
    expires_at: Date.now() + hours * 3_600_000,
  };
}

function routes(cards: ReturnType<typeof card>[]): (url: string) => unknown {
  return (url: string) => {
    if (url === '/api/usage/me') return { unlimited: false, windows: [] };
    if (url === '/api/usage/me/history') return { totals: emptyTotals, turns: [] };
    if (url === '/api/usage/cards') return { cards };
    throw new Error(`unexpected ${url}`);
  };
}

function cardRows(): HTMLElement[] {
  return [...document.querySelectorAll<HTMLElement>('.oa-card-list .oa-card-row')];
}

describe('held reset cards', () => {
  it('stacks cards separately by name and windows variant', async () => {
    get.mockImplementation(async (url) =>
      routes([
        card(1, 'VIP Card', ['5h']),
        card(2, 'VIP Card', ['5h']),
        card(3, 'VIP Card', ['1w']),
        card(4, undefined, ['5h']),
      ])(url));

    mountPanel();
    await settle();

    const rows = cardRows();
    expect(rows).toHaveLength(3);
    // VIP Card with 5h has 2 cards
    expect(rows[0]!.querySelector('.oa-card-title')!.textContent).toContain('VIP Card');
    expect(rows[0]!.querySelector('.oa-card-title')!.textContent).toContain('× 2');
    expect(rows[0]!.querySelector('.oa-card-sub')!.textContent).toContain('5-hour');

    // VIP Card with 1w
    expect(rows[1]!.querySelector('.oa-card-title')!.textContent).toContain('VIP Card');
    expect(rows[1]!.querySelector('.oa-card-sub')!.textContent).toContain('1-week');

    // Unnamed card with 5h has scope as title
    expect(rows[2]!.querySelector('.oa-card-title')!.textContent).toContain('5-hour');
  });

  it('stacks the ones that run out on the same day into a single row', async () => {
    // Three within the pinned day, and one deliberately a week out.
    get.mockImplementation(async (url) =>
      routes([card(1), card(2), card(3), card(24 * 7)])(url));

    mountPanel();
    await settle();

    const rows = cardRows();
    expect(rows).toHaveLength(2);
    expect(rows[0]!.textContent).toContain('× 3');
    // A lone card is not "× 1": the multiplier exists to make a pile legible
    // and says nothing on a row that is already one thing.
    expect(rows[1]!.textContent).not.toContain('×');
  });

  it('says how many are left beside the heading', async () => {
    get.mockImplementation(async (url) => routes([card(1), card(2), card(24 * 7)])(url));

    mountPanel();
    await settle();

    expect(document.querySelector('.oa-card-total')?.textContent).toContain('3');
  });

  it('spends the soonest card in a stack, not whichever was listed first', async () => {
    // Newest first from the server, so "the first one in the array" and "the
    // one that expires first" are different cards.
    get.mockImplementation(async (url) => routes([card(5), card(2), card(9)])(url));
    post.mockResolvedValue(undefined);

    mountPanel();
    await settle();

    const rows = cardRows();
    expect(rows).toHaveLength(1);
    rows[0]!.querySelector('button')!.click();
    await settle();

    expect(post).toHaveBeenCalledWith('/api/usage/cards/card-2/use', {});
  });

  it('refreshes the totals and the cards, not only the allowance bars', async () => {
    get.mockImplementation(async (url) => routes([card(3)])(url));

    mountPanel();
    await settle();
    expect(get).toHaveBeenCalledTimes(3);
    get.mockClear();

    const refresh = [...document.querySelectorAll<HTMLButtonElement>('button')]
      .find((node) => node.getAttribute('aria-label') === 'Refresh');
    expect(refresh).toBeDefined();
    refresh!.click();
    await settle();

    const asked = get.mock.calls.map(([url]) => url);
    expect(asked).toContain('/api/usage/me');
    expect(asked).toContain('/api/usage/me/history');
    expect(asked).toContain('/api/usage/cards');
  });
});
