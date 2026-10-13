// A detail that fails to load must not leave the panel able to save. The
// panel opens on the row the list already has, and the form still holds the
// account that was open before it: Save over that would write one account's
// values onto another.

import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import { createApp, h, nextTick, shallowRef, type App } from 'vue';
import { createMemoryHistory, createRouter } from 'vue-router';
import { adminApi, type Account } from '../src/admin/api';
import { providePanelHost } from '../src/composables/usePanelHost';
import AdminUsers from '../src/views/admin/AdminUsers.vue';
import { provideAdminView } from '../src/views/admin/adminView';

const settle = async () => {
  await new Promise((resolve) => setTimeout(resolve, 0));
  await nextTick();
};

const account = (id: string, over: Partial<Account> = {}): Account => ({
  id, username: `user-${id}`, email: `${id}@example.test`, nickname: `Nick ${id}`, avatar: '', bio: '',
  role: 'user', group_id: '', group_expires_at: 0, group_name: '',
  status: 'active', ban_reason: '', created_at: 1, updated_at: 1, last_login_at: 0,
  email_verified: true, allow_stats: true, allow_delete_conversations: true,
  api_restricted: false, api_restricted_until: 0, api_restriction_source: '',
  ...over,
}) as Account;

type Detail = Awaited<ReturnType<typeof adminApi.user>>;
const detailOf = (row: Account): Detail => ({
  user: row,
  usage: { windows: [], unlimited: true } as unknown as Detail['usage'],
  lifetime: { requests: 0, total_tokens: 0, credits: 0 } as unknown as Detail['lifetime'],
  models: [],
  policy: {} as Detail['policy'],
  cards: { available: 0, expired: 0, used: 0, total: 0, cards: [] } as unknown as Detail['cards'],
});

describe('AdminUsers, when an account detail fails to load', () => {
  let app: App | undefined;
  let host: HTMLElement;
  let panels: HTMLElement;

  async function mount(rows: Account[], failing: string): Promise<void> {
    vi.spyOn(adminApi, 'groupOptions').mockResolvedValue({ groups: [] });
    vi.spyOn(adminApi, 'users').mockResolvedValue({ users: rows, total: rows.length });
    vi.spyOn(adminApi, 'user').mockImplementation(async (id: string) => {
      if (id === failing) throw new Error('detail unavailable');
      return detailOf(rows.find((row) => row.id === id)!);
    });
    vi.spyOn(adminApi, 'userKeys').mockResolvedValue({ keys: [] });
    vi.spyOn(adminApi, 'userSessions').mockResolvedValue({ sessions: [] });
    const update = vi.spyOn(adminApi, 'updateUser').mockResolvedValue({ user: rows[0]! });

    const router = createRouter({ history: createMemoryHistory(), routes: [{ path: '/:pathMatch(.*)*', component: { render: () => null } }] });
    app = createApp({
      setup() {
        providePanelHost(shallowRef(panels));
        provideAdminView({ actionsHost: document.createElement('div'), setTitle() {}, reload() {}, params: [] });
        return () => h(AdminUsers);
      },
    });
    app.use(router);
    app.mount(host);
    await settle();
    expect(update).not.toHaveBeenCalled();
  }

  const rowOf = (id: string) => [...host.querySelectorAll<HTMLTableRowElement>('tbody tr')]
    .find((row) => row.textContent?.includes(`@user-${id}`))!;
  const footerSave = () => panels.querySelector<HTMLButtonElement>('.oa-panel-foot .oa-btn.primary');

  beforeEach(() => {
    host = document.createElement('div');
    panels = document.createElement('div');
    document.body.append(host, panels);
  });

  afterEach(() => {
    app?.unmount();
    app = undefined;
    host.remove();
    panels.remove();
    vi.restoreAllMocks();
  });

  it('offers no Save for the failed account, even after another account loaded', async () => {
    await mount([account('a'), account('b')], 'b');
    rowOf('a').click();
    await settle();
    expect(footerSave()).not.toBeNull();

    rowOf('b').click();
    await settle();

    expect(panels.textContent).toContain('detail unavailable');
    expect(footerSave()).toBeNull();
    expect(adminApi.updateUser).not.toHaveBeenCalled();
  });

  it('offers no Save when the very first account fails, before any form was filled', async () => {
    await mount([account('b')], 'b');
    rowOf('b').click();
    await settle();

    expect(panels.textContent).toContain('detail unavailable');
    expect(footerSave()).toBeNull();
    expect(adminApi.updateUser).not.toHaveBeenCalled();
  });
});
