import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import { createApp, h, nextTick, shallowRef, type App } from 'vue';
import { createMemoryHistory, createRouter, type Router } from 'vue-router';
import AdminLogs from '../src/views/admin/AdminLogs.vue';
import { adminApi, type LogFacets } from '../src/admin/api';
import { providePanelHost } from '../src/composables/usePanelHost';
import { changeLanguage, t } from '../src/composables/useI18n';
import { provideAdminView } from '../src/views/admin/adminView';

let app: App | undefined;
let host: HTMLElement;
let panels: HTMLElement;
let actions: HTMLElement;

const noFacets: LogFacets = {
  users: [], models: [], error_codes: [], statuses: [], total: 0, dropped: 0, oldest: 0,
} as unknown as LogFacets;

beforeEach(async () => {
  await changeLanguage('en');
  host = document.createElement('div');
  panels = document.createElement('div');
  actions = document.createElement('div');
  document.body.append(host, panels, actions);
  vi.spyOn(adminApi, 'logFacets').mockResolvedValue(noFacets);
  vi.spyOn(adminApi, 'logs').mockResolvedValue({ entries: [], total: 0, limit: 20, offset: 0 });
});

afterEach(() => {
  app?.unmount();
  app = undefined;
  document.body.textContent = '';
  vi.restoreAllMocks();
});

async function settle(): Promise<void> {
  await new Promise((resolve) => setTimeout(resolve, 0));
  await nextTick();
}

/** Opens the page on an address, the way the model panel's error-history link does. */
async function mountAt(address: string): Promise<Router> {
  const router = createRouter({
    history: createMemoryHistory(),
    routes: [{ path: '/:pathMatch(.*)*', component: { render: () => null } }],
  });
  await router.push(address);
  await router.isReady();
  app = createApp({ setup() {
    providePanelHost(shallowRef(panels));
    provideAdminView({ actionsHost: actions, setTitle: () => {}, reload: () => {}, params: [] });
    return () => h(AdminLogs);
  } });
  app.use(router);
  app.mount(host);
  await settle();
  return router;
}

describe('AdminLogs, clearing the filters', () => {
  // Clear used to drop the window from the address, so the route watch read
  // it back as the 24-hour default. The window is a choice, not a filter, so
  // Clear must leave it where the operator put it.
  it('keeps the chosen window when the filters are cleared', async () => {
    await mountAt('/admin/logs?model_id=m1&window=168');
    expect(adminApi.logs).toHaveBeenLastCalledWith(expect.stringContaining('model_id=m1'));

    const clear = [...host.querySelectorAll<HTMLButtonElement>('button')]
      .find((button) => button.textContent?.trim() === t('logClear'))!;
    clear.click();
    await settle();

    const last = vi.mocked(adminApi.logs).mock.lastCall![0];
    expect(last).not.toContain('model_id=');
    const since = Number(new URLSearchParams(last.slice(1)).get('since'));
    const weekAgo = Date.now() - 168 * 60 * 60 * 1000;
    expect(Math.abs(since - weekAgo)).toBeLessThan(60_000);
  });
});

describe('AdminLogs, the facets of the window on screen', () => {
  // Each window change asks for its own facets, and the two answers can come
  // back in either order. The answer for an earlier window that lands last
  // must not replace the one the operator is looking at.
  it('drops a facets answer that lands after a newer window was asked for', async () => {
    const waiting: Array<(facets: LogFacets) => void> = [];
    vi.mocked(adminApi.logFacets).mockImplementation(() => new Promise<LogFacets>((resolve) => {
      waiting.push(resolve);
    }));
    const router = await mountAt('/admin/logs?window=24');
    waiting.shift()!({ ...noFacets, total: 1 });
    await settle();

    await router.push('/admin/logs?window=1');
    await router.push('/admin/logs?window=720');
    await settle();
    expect(waiting).toHaveLength(2);

    const [older, newer] = waiting;
    newer!({ ...noFacets, total: 300 });
    await settle();
    older!({ ...noFacets, total: 100 });
    await settle();

    expect(host.textContent).toContain(t('logHolding', { count: 300 }));
    expect(host.textContent).not.toContain(t('logHolding', { count: 100 }));
  });
});
