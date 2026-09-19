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

    expect(get).toHaveBeenCalledTimes(3);
    expect(document.body.textContent).toContain('1');

    requests = 7;
    await advance(REFRESH_MS);

    expect(get).toHaveBeenCalledTimes(6);
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
});
