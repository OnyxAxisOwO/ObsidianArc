import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import { createApp, h, nextTick, shallowRef, type App } from 'vue';
import { createMemoryHistory, createRouter, type Router } from 'vue-router';
import AdminAudit from '../src/views/admin/AdminAudit.vue';
import { adminApi, type AuditEntry, type AuditFacets } from '../src/admin/api';
import { ApiError } from '../src/api/client';
import { providePanelHost } from '../src/composables/usePanelHost';
import { changeLanguage, t } from '../src/composables/useI18n';
import { provideAdminView } from '../src/views/admin/adminView';
import { auditText as at } from '../src/views/admin/audit-text';

let app: App | undefined;
let host: HTMLElement;
let panels: HTMLElement;
let actions: HTMLElement;

const noFacets: AuditFacets = { actors: [], routes: [], total: 0, failed: 0, retention_days: 180 };

function entry(id: string, actor: string): AuditEntry {
  return {
    id, at: Date.now(), actor_id: `u-${actor}`, actor_username: actor, actor_role: 'admin',
    method: 'POST', route: '/api/admin/example', path: '/api/admin/example',
    target_id: '', target_label: '', status: 200, duration_ms: 3, via: 'web', ip: '', user_agent: '',
  };
}

beforeEach(async () => {
  await changeLanguage('en');
  host = document.createElement('div');
  panels = document.createElement('div');
  actions = document.createElement('div');
  document.body.append(host, panels, actions);
  vi.spyOn(adminApi, 'auditFacets').mockResolvedValue(noFacets);
  vi.spyOn(adminApi, 'audit').mockResolvedValue({ entries: [], total: 0, limit: 20, offset: 0 });
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
    return () => h(AdminAudit);
  } });
  app.use(router);
  app.mount(host);
  await settle();
  return router;
}

function lastListQuery(): URLSearchParams {
  return new URLSearchParams(vi.mocked(adminApi.audit).mock.lastCall![0].slice(1));
}

describe('AdminAudit, the all-time window', () => {
  // All time is the empty value, written as `?window=`. Reading the empty
  // value as missing turned it back into seven days, so the choice never
  // reached the list and a reload of the page lost it.
  it('asks for the whole trail when the address says all time', async () => {
    await mountAt('/admin/audit?window=');
    expect(lastListQuery().has('since')).toBe(false);
    expect(vi.mocked(adminApi.auditFacets).mock.lastCall![0]).toBe('');
  });

  it('moves from the default week to all time', async () => {
    const router = await mountAt('/admin/audit');
    expect(lastListQuery().has('since')).toBe(true);

    await router.replace('/admin/audit?window=');
    await settle();
    expect(lastListQuery().has('since')).toBe(false);
  });

  it('falls back to the week for a window it does not know', async () => {
    await mountAt('/admin/audit?window=nonsense');
    const since = Number(lastListQuery().get('since'));
    expect(Math.abs(since - (Date.now() - 168 * 3_600_000))).toBeLessThan(60_000);
  });
});

describe('AdminAudit, what a filter asks for', () => {
  // The facets are counted over the window alone, so a filter that leaves
  // the window where it was has no reason to count them again.
  it('repaints the list without recounting the facets when only a filter moves', async () => {
    const router = await mountAt('/admin/audit');
    const facetsCalls = vi.mocked(adminApi.auditFacets).mock.calls.length;

    await router.replace('/admin/audit?outcome=failed');
    await settle();
    expect(lastListQuery().get('outcome')).toBe('failed');
    expect(vi.mocked(adminApi.auditFacets).mock.calls.length).toBe(facetsCalls);

    await router.replace('/admin/audit?outcome=failed&window=24');
    await settle();
    expect(vi.mocked(adminApi.auditFacets).mock.calls.length).toBe(facetsCalls + 1);
  });

  // Two filters chosen in quick succession ask twice, and the answers can
  // land in either order; the one for the earlier filter must not replace
  // the list the operator is now looking at.
  it('drops a list answer that lands after a newer one was asked for', async () => {
    const router = await mountAt('/admin/audit');
    const waiting: Array<(rows: { entries: AuditEntry[]; total: number; limit: number; offset: number }) => void> = [];
    vi.mocked(adminApi.audit).mockImplementation(() => new Promise((resolve) => { waiting.push(resolve); }));

    await router.replace('/admin/audit?outcome=ok');
    await settle();
    await router.replace('/admin/audit?outcome=failed');
    await settle();
    expect(waiting).toHaveLength(2);

    const [older, newer] = waiting;
    newer!({ entries: [entry('b', 'newer-actor')], total: 1, limit: 20, offset: 0 });
    await settle();
    older!({ entries: [entry('a', 'older-actor')], total: 1, limit: 20, offset: 0 });
    await settle();

    expect(host.textContent).toContain('newer-actor');
    expect(host.textContent).not.toContain('older-actor');
  });
});

describe('AdminAudit, a page the trail shrank out from under', () => {
  // The trail on page two can shrink below it — retention trims it, or the
  // seven days move on — and the server answers an offset past the end with
  // nothing. With one page left the pager draws no arrows, so the operator
  // had no way back but to change a filter.
  it('asks for the last page that still exists', async () => {
    const rows = (count: number) => Array.from({ length: count }, (_, i) => entry(`e${i}`, `actor${i}`));
    vi.mocked(adminApi.audit).mockResolvedValue({ entries: rows(20), total: 25, limit: 20, offset: 0 });
    await mountAt('/admin/audit');

    vi.mocked(adminApi.audit).mockImplementation(async (query) => {
      const offset = Number(new URLSearchParams(query.slice(1)).get('offset'));
      return offset >= 15
        ? { entries: [], total: 15, limit: 20, offset }
        : { entries: rows(15), total: 15, limit: 20, offset };
    });
    [...host.querySelectorAll<HTMLButtonElement>('.oa-pagination button')]
      .find((button) => button.textContent?.trim() === t('next'))!.click();
    await settle();

    expect(lastListQuery().get('offset')).toBe('0');
    expect(host.textContent).toContain('actor14');
  });
});

describe('AdminAudit, the facets of the window on screen', () => {
  // The same race the request log guards against: each window asks for its own
  // facets, and an answer for an earlier window that lands last must not stand.
  it('drops a facets answer that lands after a newer window was asked for', async () => {
    const waiting: Array<(facets: AuditFacets) => void> = [];
    vi.mocked(adminApi.auditFacets).mockImplementation(() => new Promise<AuditFacets>((resolve) => {
      waiting.push(resolve);
    }));
    const router = await mountAt('/admin/audit?window=24');
    waiting.shift()!({ ...noFacets, total: 1 });
    await settle();

    await router.push('/admin/audit?window=168');
    await router.push('/admin/audit?window=720');
    await settle();
    expect(waiting).toHaveLength(2);

    const [older, newer] = waiting;
    newer!({ ...noFacets, total: 300 });
    await settle();
    older!({ ...noFacets, total: 100 });
    await settle();

    expect(host.textContent).toContain(at('summary', { actors: 0, total: '300' }));
    expect(host.textContent).not.toContain(at('summary', { actors: 0, total: '100' }));
  });

  // A failed facets answer used to replace the whole trail with the failure.
  it('keeps the trail on screen when the facets fail, and shows the failure where the filters are', async () => {
    vi.mocked(adminApi.auditFacets).mockRejectedValue(new ApiError(500, 'internal', 'facets went away'));
    vi.mocked(adminApi.audit).mockResolvedValue({ entries: [entry('a', 'kept-actor')], total: 1, limit: 20, offset: 0 });
    await mountAt('/admin/audit');

    expect(host.textContent).toContain('kept-actor');
    expect(host.querySelector('#auditFilter')).toBeNull();
    expect(host.textContent).toContain(t('couldNotLoad'));
  });
});
