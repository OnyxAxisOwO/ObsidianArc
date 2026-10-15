// Auto-refresh on the two usage surfaces: the user's panel and the admin
// page. Both re-read themselves on a timer while mounted, both keep the last
// good figures on screen when a refresh fails, and both repaint once the
// server has newer numbers — including the case where the *first* read
// failed and a later refresh is the first success, which must not leave the
// page on "loading" or on the error forever.
//
// The API is mocked at the client module, so everything above it — the
// components' fetch paths, the interval, the stale-response guard — is the
// real code under test. Mounts are plain createApp + jsdom, the same harness
// the other component tests use.

import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import { createApp, h, nextTick, ref, type App } from 'vue';
import { ApiError, api } from '@/api/client';
import { providePanelHost } from '@/composables/usePanelHost';
import UsagePanel from '@/views/UsagePanel.vue';
import AdminUsage from '@/views/admin/AdminUsage.vue';
import { provideAdminView } from '@/views/admin/adminView';
import { toggleCategory, toggleSafeMode } from '@/admin/safeMode';
import { t } from '@/composables/useI18n';

// Both surfaces agree on the pace, so the test pins the number rather than
// importing it from either view.
const REFRESH_MS = 15000;

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
  },
}));

// The panel pushes a route on close; nothing here navigates for real.
vi.mock('vue-router', () => ({
  useRouter: () => ({ push: vi.fn() }),
}));

const get = vi.mocked(api.get);

let app: App | null = null;
let host: HTMLElement;

beforeEach(() => {
  vi.useFakeTimers();
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

/** Advances the fake timer and lets the fired refreshes settle and repaint. */
async function advance(ms: number): Promise<void> {
  await vi.advanceTimersByTimeAsync(ms);
  await nextTick();
}

/** A ledger total with everything at zero except the request count. */
function totals(requests: number) {
  return {
    requests,
    input_tokens: 0,
    output_tokens: 0,
    reasoning_tokens: 0,
    total_tokens: 0,
    credits: 0,
    errors: 0,
  };
}

function panelRoutes(history: unknown, cards: unknown = { cards: [] }): (url: string) => unknown {
  return (url: string) => {
    if (url === '/api/usage/me') return { unlimited: false, windows: [] };
    if (url === '/api/usage/me/history') return history;
    if (url === '/api/usage/cards') return cards;
    if (url === '/api/bonus') return { bars: [] };
    if (url === '/api/checkin') return { enabled: false };
    throw new Error(`unexpected ${url}`);
  };
}

describe('UsagePanel', () => {
  /** The panel teleports into a host the chat row provides; so does this. */
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

  it('re-reads allowance, history and cards while open', async () => {
    let requests = 1;
    get.mockImplementation(async (url) =>
      panelRoutes({ totals: totals(requests), turns: [] })(url));

    mountPanel();
    await advance(0);

    // The allowance, the history, the cards, the bonus bars and the check-in
    // (which is read once: it changes when its own button is pressed).
    expect(get).toHaveBeenCalledTimes(5);
    expect(document.body.textContent).toContain('1');

    requests = 7;
    await advance(REFRESH_MS);

    expect(get).toHaveBeenCalledTimes(9);
    expect(document.body.textContent).toContain('7');
  });

  it('keeps the last good figures when a refresh fails', async () => {
    let allowanceReads = 0;
    get.mockImplementation(async (url: string) => {
      if (url === '/api/usage/me') {
        allowanceReads += 1;
        if (allowanceReads > 1) throw new ApiError(500, 'internal', 'boom');
        return { unlimited: false, windows: [] };
      }
      return panelRoutes({ totals: totals(3), turns: [] })(url);
    });

    mountPanel();
    await advance(0);
    await advance(REFRESH_MS);

    // The failed re-read did not trade the figures for an error screen.
    expect(document.body.textContent).not.toContain('boom');
    expect(document.body.textContent).toContain('3');
  });

  it('recovers on its own once the server answers again', async () => {
    let down = true;
    get.mockImplementation(async (url: string) => {
      if (url === '/api/usage/me/history') {
        if (down) throw new ApiError(502, 'bad_gateway', 'boom');
        return { totals: totals(5), turns: [] };
      }
      return panelRoutes({ totals: totals(0), turns: [] })(url);
    });

    mountPanel();
    await advance(0);
    expect(document.body.textContent).toContain('boom');

    down = false;
    await advance(REFRESH_MS);

    expect(document.body.textContent).not.toContain('boom');
    expect(document.body.textContent).toContain('5');
  });

  it('does not put back a card spent while an earlier periodic read was on its way', async () => {
    const card = { id: 'c1', source: 'grant', expires_at: Date.now() + 86_400_000, windows: [] };
    let cardReads = 0;
    let releaseStale!: (value: unknown) => void;
    get.mockImplementation(async (url: string) => {
      if (url === '/api/usage/cards') {
        cardReads += 1;
        if (cardReads === 1) return { cards: [card] };
        // The periodic read that was asked for before the spend, held open.
        if (cardReads === 2) return new Promise((resolve) => { releaseStale = resolve; });
        return { cards: [] };
      }
      return panelRoutes({ totals: totals(0), turns: [] })(url);
    });
    vi.mocked(api.post).mockResolvedValue(undefined as never);

    mountPanel();
    await advance(0);
    const useButton = (): HTMLButtonElement | undefined =>
      [...document.body.querySelectorAll<HTMLButtonElement>('button')]
        .find((node) => node.textContent?.trim() === t('cardUse'));
    expect(useButton()).toBeDefined();

    await advance(REFRESH_MS);
    useButton()!.click();
    await advance(0);
    expect(cardReads).toBe(3);

    releaseStale({ cards: [card] });
    await advance(0);

    expect(useButton()).toBeUndefined();
  });
});

/** Enough of the admin shell for a page that only names itself and renders. */
function mountAdmin(): void {
  const actionsHost = document.createElement('div');
  document.body.appendChild(actionsHost);
  app = createApp({
    setup() {
      provideAdminView({
        setTitle: () => {},
        reload: () => {},
        params: [],
        actionsHost,
      });
    },
    render: () => h(AdminUsage),
  });
  app.mount(host);
}

function adminRoutes(usage: unknown): (url: string) => unknown {
  return (url: string) => {
    if (url.startsWith('/api/admin/usage?')) return usage;
    if (url.startsWith('/api/admin/usage/records')) return { records: [], total: 0 };
    throw new Error(`unexpected ${url}`);
  };
}

function adminPayload(requests: number) {
  return {
    totals: totals(requests),
    by_model: [],
    by_provider: [],
    by_user: [],
    series: [],
    bucket_ms: 3600000,
  };
}

describe('AdminUsage', () => {
  it('re-reads the aggregates while open', async () => {
    let requests = 1;
    get.mockImplementation(async (url) => adminRoutes(adminPayload(requests))(url));

    mountAdmin();
    await advance(0);
    expect(document.body.textContent).toContain('1');

    requests = 9;
    await advance(REFRESH_MS);
    expect(document.body.textContent).toContain('9');
  });

  it('keeps the last good figures when a refresh fails', async () => {
    let reads = 0;
    get.mockImplementation(async (url: string) => {
      if (url.startsWith('/api/admin/usage?')) {
        reads += 1;
        if (reads > 1) throw new ApiError(500, 'internal', 'boom');
        return adminPayload(6);
      }
      return adminRoutes(null)(url);
    });

    mountAdmin();
    await advance(0);
    await advance(REFRESH_MS);

    expect(document.body.textContent).not.toContain('boom');
    expect(document.body.textContent).toContain('6');
  });

  it('recovers on its own once the server answers again', async () => {
    let down = true;
    get.mockImplementation(async (url: string) => {
      if (url.startsWith('/api/admin/usage?')) {
        if (down) throw new ApiError(502, 'bad_gateway', 'boom');
        return adminPayload(8);
      }
      return adminRoutes(null)(url);
    });

    mountAdmin();
    await advance(0);
    expect(document.body.textContent).toContain('boom');

    down = false;
    await advance(REFRESH_MS);

    expect(document.body.textContent).not.toContain('boom');
    expect(document.body.textContent).toContain('8');
  });

  // A hidden tab recomputing every aggregate four times a minute is load with
  // no reader. It waits, and catches up the moment it is looked at.
  it('pauses while the tab is hidden and catches up when it is shown', async () => {
    let reads = 0;
    get.mockImplementation(async (url: string) => {
      if (url.startsWith('/api/admin/usage?')) { reads += 1; return adminPayload(reads); }
      return adminRoutes(null)(url);
    });
    let state: DocumentVisibilityState = 'hidden';
    const visibility = vi.spyOn(document, 'visibilityState', 'get').mockImplementation(() => state);
    try {
      mountAdmin();
      await advance(0);
      await advance(REFRESH_MS * 3);
      expect(reads).toBe(1);

      state = 'visible';
      document.dispatchEvent(new Event('visibilitychange'));
      await advance(0);
      expect(reads).toBe(2);
    } finally {
      visibility.mockRestore();
    }
  });

  it('aborts in-flight usage and records requests when range changes quickly', async () => {
    let resolveFirst!: (val: unknown) => void;
    const firstPromise = new Promise((resolve) => { resolveFirst = resolve; });
    const usageSignals: AbortSignal[] = [];

    get.mockImplementation(async (url: string, options?: { signal?: AbortSignal }) => {
      if (url.startsWith('/api/admin/usage?')) {
        if (options?.signal) usageSignals.push(options.signal);
        if (usageSignals.length === 1) return firstPromise;
        return adminPayload(10);
      }
      return adminRoutes(null)(url);
    });

    mountAdmin();
    await advance(0);
    expect(usageSignals).toHaveLength(1);
    expect(usageSignals[0]?.aborted).toBe(false);

    // Click '7d' preset while initial load is in flight
    const rangeButtons = document.querySelectorAll('.oa-range button');
    rangeButtons[2]!.dispatchEvent(new MouseEvent('click', { bubbles: true }));
    await advance(0);

    expect(usageSignals[0]?.aborted).toBe(true);
    expect(usageSignals).toHaveLength(2);
    expect(usageSignals[1]?.aborted).toBe(false);

    resolveFirst(adminPayload(1));
  });

  it('aborts in-flight requests when unmounted', async () => {
    let resolveSlow!: (val: unknown) => void;
    const slowPromise = new Promise((resolve) => { resolveSlow = resolve; });
    let activeSignal: AbortSignal | undefined;

    get.mockImplementation(async (url: string, options?: { signal?: AbortSignal }) => {
      if (url.startsWith('/api/admin/usage?')) {
        activeSignal = options?.signal;
        return slowPromise;
      }
      return adminRoutes(null)(url);
    });

    mountAdmin();
    await advance(0);
    expect(activeSignal).toBeDefined();
    expect(activeSignal?.aborted).toBe(false);

    app?.unmount();
    app = null;
    expect(activeSignal?.aborted).toBe(true);

    resolveSlow(adminPayload(1));
  });

  it('aborts in-flight records requests when range changes quickly', async () => {
    let resolveRecords1!: (val: unknown) => void;
    const records1Promise = new Promise((resolve) => { resolveRecords1 = resolve; });
    const recordsSignals: AbortSignal[] = [];

    get.mockImplementation(async (url: string, options?: { signal?: AbortSignal }) => {
      if (url.startsWith('/api/admin/usage?')) return adminPayload(10);
      if (url.startsWith('/api/admin/usage/records')) {
        if (options?.signal) recordsSignals.push(options.signal);
        if (recordsSignals.length === 1) return records1Promise;
        return { records: [], total: 100 };
      }
      return adminRoutes(null)(url);
    });

    mountAdmin();
    await advance(0);
    expect(recordsSignals).toHaveLength(1);
    expect(recordsSignals[0]?.aborted).toBe(false);

    // Switch range preset to 7d while records request is in flight
    const rangeButtons = document.querySelectorAll('.oa-range button');
    rangeButtons[2]!.dispatchEvent(new MouseEvent('click', { bubbles: true }));
    await advance(0);

    expect(recordsSignals[0]?.aborted).toBe(true);
    expect(recordsSignals).toHaveLength(2);
    expect(recordsSignals[1]?.aborted).toBe(false);

    resolveRecords1({ records: [], total: 100 });
  });

  it('does not display error banner when requests are aborted', async () => {
    get.mockImplementation(async (url: string, options?: { signal?: AbortSignal }) => {
      if (url.startsWith('/api/admin/usage?')) {
        if (options?.signal?.aborted) {
          throw new DOMException('The operation was aborted.', 'AbortError');
        }
        return new Promise((_, reject) => {
          options?.signal?.addEventListener('abort', () => {
            reject(new DOMException('The operation was aborted.', 'AbortError'));
          });
        });
      }
      return adminRoutes(null)(url);
    });

    mountAdmin();
    await advance(0);

    // Switch range preset to abort initial request
    const rangeButtons = document.querySelectorAll('.oa-range button');
    rangeButtons[2]!.dispatchEvent(new MouseEvent('click', { bubbles: true }));
    await advance(0);

    expect(document.querySelector('.oa-banner-error')).toBeNull();
  });
});

// A ULID, the only shape the page accepts from a link as a filter.
const MODEL_ID = '01ARZ3NDEKTSV4RRFFQ69G5FAV';
const ACCOUNT_ID = '01BX5ZZKBKACTAV9WEVGEMMVRZ';

describe('AdminUsage windows and filters', () => {
  afterEach(() => {
    window.history.replaceState(null, '', '/');
    toggleSafeMode(false);
    toggleCategory('users', false);
    toggleCategory('providers', false);
  });

  // A preset is the last N hours as of the request. A page left open must ask
  // for the window that ends now, not the one that ended when it was opened.
  it('asks for a preset window that moves forward while the page is open', async () => {
    const since: number[] = [];
    get.mockImplementation(async (url: string) => {
      if (url.startsWith('/api/admin/usage?')) {
        since.push(Number(new URL(url, 'http://localhost').searchParams.get('since')));
        return adminPayload(1);
      }
      return adminRoutes(null)(url);
    });

    mountAdmin();
    await advance(0);
    // The 24-hour preset is the second button in the range row.
    document.querySelectorAll<HTMLButtonElement>('.oa-range button')[1]!.dispatchEvent(new MouseEvent('click', { bubbles: true }));
    await advance(0);
    const opened = since.at(-1)!;

    vi.setSystemTime(Date.now() + 3 * 3600_000);
    await advance(REFRESH_MS);
    const refreshed = since.at(-1)!;

    expect(refreshed).toBeGreaterThan(opened);
    expect(Math.abs(refreshed - (Date.now() - 24 * 3600_000))).toBeLessThan(REFRESH_MS);
  });

  // The header's live rate is the filtered figure the summary carries. The
  // poll has to ask under the same filters, or the two alternate every tick.
  it('reads the live rate under the same filters as the figures beside it', async () => {
    window.history.replaceState(null, '', `/admin/usage?model=${MODEL_ID}`);
    const rates: string[] = [];
    get.mockImplementation(async (url: string) => {
      if (url.startsWith('/api/admin/usage/rpm')) {
        rates.push(url);
        return { rpm: 2 };
      }
      if (url.startsWith('/api/admin/usage?')) return adminPayload(1);
      return adminRoutes(null)(url);
    });

    mountAdmin();
    await advance(0);
    await advance(10_000);

    expect(rates.length).toBeGreaterThan(0);
    expect(rates.every((url) => url.includes(`model_id=${MODEL_ID}`))).toBe(true);
  });

  // The page refreshes every fifteen seconds. A refresh that brings the same
  // models must not reset the menu someone is moving through.
  it('keeps an open model menu where it was through a refresh', async () => {
    const models = ['alpha', 'beta', 'gamma', 'delta'].map((key) => ({
      key, label: `Model ${key}`, detail: '', requests: 1, users: 1, models: 1, errors: 0, total_tokens: 0, credits: 0, input_tokens: 0, output_tokens: 0, reasoning_tokens: 0,
    }));
    get.mockImplementation(async (url: string) => {
      if (url.startsWith('/api/admin/usage?')) return { ...adminPayload(4), by_model: models };
      return adminRoutes(null)(url);
    });

    mountAdmin();
    await advance(0);
    const trigger = document.querySelector<HTMLElement>('.oa-filter-select .oa-select') ?? document.querySelector<HTMLElement>('.oa-filter-select');
    expect(trigger).not.toBeNull();
    trigger!.click();
    await advance(0);
    for (let i = 0; i < 2; i++) {
      trigger!.dispatchEvent(new KeyboardEvent('keydown', { key: 'ArrowDown', bubbles: true }));
    }
    await advance(0);
    const optionsBefore = [...document.querySelectorAll<HTMLElement>('[role="option"]')];
    expect(optionsBefore.length).toBeGreaterThan(2);
    expect(optionsBefore[2]!.classList.contains('active')).toBe(true);

    await advance(REFRESH_MS);

    const optionsAfter = [...document.querySelectorAll<HTMLElement>('[role="option"]')];
    expect(optionsAfter[2]!.classList.contains('active')).toBe(true);
    expect(optionsAfter[0]!.classList.contains('active')).toBe(false);
  });

  it('does not write an account name into the focus banner under safe mode', async () => {
    window.history.replaceState(null, '', `/admin/usage?user=${ACCOUNT_ID}`);
    toggleSafeMode(true);
    toggleCategory('users', true);
    get.mockImplementation(async (url: string) => {
      if (url.startsWith('/api/admin/usage?')) {
        return {
          ...adminPayload(4),
          by_user: [{ key: ACCOUNT_ID, label: 'Alice Example', detail: 'alice', requests: 4, users: 1, models: 1, errors: 0, total_tokens: 0, credits: 0, input_tokens: 0, output_tokens: 0, reasoning_tokens: 0 }],
        };
      }
      return adminRoutes(null)(url);
    });

    mountAdmin();
    await advance(0);

    const banner = document.querySelector<HTMLElement>('.oa-report-focus');
    expect(banner).not.toBeNull();
    expect(banner!.textContent).not.toContain('Alice');
    expect(banner!.textContent).not.toContain('alice');
    expect(banner!.getAttribute('aria-label')).not.toContain('Alice');
    expect(document.querySelectorAll<HTMLElement>('.oa-filter-select')[1]?.textContent).not.toContain('Alice');
  });

  // A model's second line is the provider serving it, and the provider menu
  // names providers outright. Both stay hidden under the providers category,
  // in the menus and in the banner a chosen model writes out.
  it('does not write a provider name into the model menu, provider menu or focus banner under safe mode', async () => {
    toggleSafeMode(true);
    toggleCategory('providers', true);
    const figures = { requests: 4, users: 1, models: 1, errors: 0, total_tokens: 0, credits: 0, input_tokens: 0, output_tokens: 0, reasoning_tokens: 0 };
    get.mockImplementation(async (url: string) => {
      if (url.startsWith('/api/admin/usage?')) {
        return {
          ...adminPayload(4),
          by_model: [{ key: MODEL_ID, label: 'Model Alpha', detail: 'Provider Zeta', ...figures }],
          by_provider: [{ key: 'zeta', label: 'Provider Zeta', detail: '', ...figures }],
        };
      }
      return adminRoutes(null)(url);
    });

    mountAdmin();
    await advance(0);
    const optionText = (): string => [...document.querySelectorAll<HTMLElement>('[role="option"]')].map((node) => node.textContent ?? '').join(' | ');

    // Index 0 is the model filter, 3 the provider filter. Option 0 is "all", option 1 the model.
    document.querySelectorAll<HTMLElement>('.oa-filter-select')[0]!.click();
    await advance(0);
    expect(optionText()).toContain('Model Alpha · ******');
    expect(optionText()).not.toContain('Provider Zeta');
    document.querySelectorAll<HTMLElement>('[role="option"]')[1]!.click();
    await advance(0);

    const banner = document.querySelector<HTMLElement>('.oa-report-focus');
    expect(banner).not.toBeNull();
    expect(banner!.textContent).toContain('Model Alpha');
    expect(banner!.textContent).not.toContain('Provider Zeta');

    document.querySelectorAll<HTMLElement>('.oa-filter-select')[3]!.click();
    await advance(0);
    expect(optionText()).toContain('******');
    expect(optionText()).not.toContain('Provider Zeta');
  });

  it('reads outcome labels on the outcomes board while providers are masked', async () => {
    toggleSafeMode(true);
    toggleCategory('providers', true);
    get.mockImplementation(async (url: string) => {
      if (url.startsWith('/api/admin/usage?')) {
        return {
          ...adminPayload(4),
          by_status: [{ key: 'error', label: '', detail: '', requests: 4, users: 1, models: 1, errors: 4, total_tokens: 0, credits: 0, input_tokens: 0, output_tokens: 0, reasoning_tokens: 0 }],
        };
      }
      return adminRoutes(null)(url);
    });

    mountAdmin();
    await advance(0);
    const outcomes = [...document.querySelectorAll<HTMLButtonElement>('#secSpread .oa-segment button')]
      .find((button) => button.textContent?.trim() === t('spreadOutcomes'));
    expect(outcomes).toBeDefined();
    outcomes!.click();
    await advance(0);

    expect(document.querySelector<HTMLElement>('#secSpread')?.textContent).toContain(t('statusFailed'));
  });
});

describe('AdminUsage ledger and custom range', () => {
  /** Clicks the button under root whose text is label. */
  function clickButtonLabelled(root: string, label: string): void {
    const found = [...document.querySelectorAll<HTMLButtonElement>(root)].find((node) => node.textContent?.trim() === label);
    if (!found) throw new Error(`Missing button: ${label}`);
    found.dispatchEvent(new MouseEvent('click', { bubbles: true }));
  }

  /** The custom range dialog is an OaPanel, which needs a panel host as the layout provides one. */
  function mountAdminWithPanel(): void {
    const actionsHost = document.createElement('div');
    const panelHost = document.createElement('div');
    document.body.append(actionsHost, panelHost);
    app = createApp({
      setup() {
        provideAdminView({ setTitle: () => {}, reload: () => {}, params: [], actionsHost });
        providePanelHost(ref(panelHost));
      },
      render: () => h(AdminUsage),
    });
    app.mount(host);
  }

  // A ledger that did not load is reported where the ledger is. It used to
  // replace the whole report, and the message began with the class name, since
  // String() of an error is "ApiError: ...".
  it('shows a failed ledger inside its block and keeps the figures', async () => {
    get.mockImplementation(async (url: string) => {
      if (url.startsWith('/api/admin/usage?')) return adminPayload(7);
      if (url.startsWith('/api/admin/usage/records')) throw new ApiError(500, 'internal', 'ledger is down');
      return adminRoutes(null)(url);
    });

    mountAdmin();
    await advance(0);

    const notices = document.querySelectorAll('.oa-notice-body');
    expect(notices).toHaveLength(1);
    expect(document.querySelector('#secRecords .oa-notice-body')?.textContent).toBe('ledger is down');
    expect(document.body.textContent).not.toContain('ApiError');
    expect(document.body.textContent).toContain('7');
  });

  // The picker has minute precision and the server's until is exclusive. A day
  // picked as "yesterday" has to reach the first instant of today, or its last
  // minute is left out of every figure and of the ledger.
  it('asks for the whole of a day picked as yesterday', async () => {
    const until: number[] = [];
    get.mockImplementation(async (url: string) => {
      if (url.startsWith('/api/admin/usage?')) {
        until.push(Number(new URL(url, 'http://localhost').searchParams.get('until')));
        return adminPayload(1);
      }
      return adminRoutes(null)(url);
    });

    mountAdminWithPanel();
    await advance(0);
    clickButtonLabelled('.oa-range button', t('rangeCustomShort'));
    await advance(0);
    clickButtonLabelled('.oa-chip-btn', t('presetYesterday'));
    await advance(0);
    clickButtonLabelled('button', t('apply'));
    await advance(0);

    const startOfToday = new Date();
    startOfToday.setHours(0, 0, 0, 0);
    expect(until.at(-1)).toBe(startOfToday.getTime());
  });
});

describe('AdminUsage all-time preset', () => {
  afterEach(() => {
    window.history.replaceState(null, '', '/');
  });

  // Without since the server starts the window at thirty days, so the all
  // preset has to send since=0 to get the widest window it labels itself as.
  it('asks for the widest window rather than the thirty-day default', async () => {
    const sent: URLSearchParams[] = [];
    get.mockImplementation(async (url: string) => {
      if (url.startsWith('/api/admin/usage?')) {
        sent.push(new URL(url, 'http://localhost').searchParams);
        return adminPayload(1);
      }
      return adminRoutes(null)(url);
    });

    mountAdmin();
    await advance(0);
    const all = [...document.querySelectorAll<HTMLButtonElement>('.oa-range button')]
      .find((button) => button.textContent?.trim() === t('rangeAllShort'));
    expect(all).toBeDefined();
    all!.dispatchEvent(new MouseEvent('click', { bubbles: true }));
    await advance(0);

    expect(sent.at(-1)!.has('since')).toBe(true);
    expect(sent.at(-1)!.get('since')).toBe('0');
  });
});
