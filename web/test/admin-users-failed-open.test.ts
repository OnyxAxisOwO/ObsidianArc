// A detail that fails to load must not leave the panel able to save. The
// panel opens on the row the list already has, and the form still holds the
// account that was open before it: Save over that would write one account's
// values onto another.

import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import { createApp, h, nextTick, shallowRef, type App } from 'vue';
import { createMemoryHistory, createRouter } from 'vue-router';
import { adminApi, type Account } from '../src/admin/api';
import type { ApiKey } from '../src/api/keys';
import { t } from '../src/composables/useI18n';
import { installPlugins } from '../src/plugins/registry';
import { providePanelHost } from '../src/composables/usePanelHost';
import AdminUsers from '../src/views/admin/AdminUsers.vue';
import { provideAdminView } from '../src/views/admin/adminView';
import { settle } from './helpers';
import { makeAccount } from './fixtures/account';

;

const account = (id: string, over: Partial<Account> = {}): Account => makeAccount({
  id, username: `user-${id}`, email: `${id}@example.test`, nickname: `Nick ${id}`,
  group_id: '', group_name: '', ban_reason: '',
  ...over,
});

type Detail = Awaited<ReturnType<typeof adminApi.user>>;
const detailOf = (row: Account): Detail => ({
  user: row,
  usage: { windows: [], unlimited: true } as unknown as Detail['usage'],
  lifetime: { requests: 0, total_tokens: 0, credits: 0 } as unknown as Detail['lifetime'],
  models: [],
  policy: {} as Detail['policy'],
  cards: { available: 0, expired: 0, used: 0, total: 0, cards: [] } as unknown as Detail['cards'],
});

const deferred = <T,>() => {
  let resolve!: (value: T) => void;
  const promise = new Promise<T>((done) => { resolve = done; });
  return { promise, resolve };
};

describe('AdminUsers, when an account detail fails to load', () => {
  let app: App | undefined;
  let host: HTMLElement;
  let panels: HTMLElement;

  async function mount(rows: Account[], failing: string, namesFail = false): Promise<void> {
    vi.spyOn(adminApi, 'groupOptions').mockImplementation(async () => {
      if (namesFail) throw new Error('names unavailable');
      return { groups: [] };
    });
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
    installPlugins([]);
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
  const fieldFor = (label: string) => [...panels.querySelectorAll<HTMLElement>('.oa-field')]
    .find((node) => node.querySelector('.oa-field-label')?.textContent?.trim() === label)!
    .querySelector<HTMLInputElement>('input')!;
  const buttonNamed = (label: string) => [...panels.querySelectorAll<HTMLButtonElement>('button')]
    .find((button) => button.textContent?.trim() === label)!;

  // The keys and devices are a second request after the detail. Opening
  // another account before they arrive must not put this one's under the other.
  it('does not show the keys or devices of an account that was left while they loaded', async () => {
    await mount([account('a'), account('b')], '');
    const keys = deferred<{ keys: ApiKey[] }>();
    const sessions = deferred<{ sessions: Array<{ id: string; created_at: number; last_seen_at: number; ip: string; user_agent: string }> }>();
    vi.mocked(adminApi.userKeys).mockImplementation((id: string) =>
      (id === 'a' ? keys.promise : Promise.resolve({ keys: [] })));
    vi.mocked(adminApi.userSessions).mockImplementation((id: string) =>
      (id === 'a' ? sessions.promise : Promise.resolve({ sessions: [] })));

    rowOf('a').click();
    await settle();
    rowOf('b').click();
    await settle();
    keys.resolve({ keys: [{ id: 'k1', prefix: 'sk-alpha', name: 'Alpha key' } as unknown as ApiKey] });
    sessions.resolve({ sessions: [{ id: 's1', created_at: 1, last_seen_at: 1, ip: '203.0.113.9', user_agent: 'Alpha browser' }] });
    await settle();

    expect(panels.textContent).not.toContain('sk-alpha');
    expect(panels.textContent).not.toContain('203.0.113.9');
  });

  // The grant has happened when the card list is re-read. A failed re-read is
  // not a failed grant: the result stays, and the button is free again.
  it('keeps the grant result and a usable Grant button when only the card re-read fails', async () => {
    await mount([account('a')], '');
    rowOf('a').click();
    await settle();
    vi.spyOn(adminApi, 'grantCards').mockResolvedValue(undefined);
    vi.mocked(adminApi.user).mockRejectedValueOnce(new Error('reread failed'));

    buttonNamed(t('grantCards')).click();
    await settle();

    expect(panels.textContent).toContain(t('granted', { count: 1 }));
    expect(panels.textContent).not.toContain('reread failed');
    expect(buttonNamed(t('grantCards')).disabled).toBe(false);
  });

  // A plugin action that keeps the panel open re-reads the figures; it must not
  // rebuild the form from the server and discard what the operator typed.
  it('keeps unsaved edits when a plugin action finishes and the panel stays open', async () => {
    installPlugins([{ name: 'ops', userActions: [{
      id: 'quota',
      title: () => 'Quota',
      buttons: [{ label: () => 'Reset quota', confirm: () => 'Reset quota?', run: async () => 'Quota reset' }],
    }] }]);
    await mount([account('a')], '');
    rowOf('a').click();
    await settle();
    const nickname = fieldFor(t('nickname'));
    nickname.value = 'Edited';
    nickname.dispatchEvent(new Event('input', { bubbles: true }));
    await nextTick();

    const action = buttonNamed('Reset quota');
    action.click();
    await nextTick();
    action.click();
    await settle();

    expect(panels.textContent).toContain('Quota reset');
    expect(fieldFor(t('nickname')).value).toBe('Edited');
  });
  // The moved count is a result, not the button's own label: once the cards are
  // re-read the button offers the reschedule again, with the count it has now.
  it('reads the movable count on the reschedule button again after a reschedule', async () => {
    await mount([account('a')], '');
    // The server's view of the unused cards changes when the reschedule lands,
    // so the re-read must come back with a different count. A count that was
    // never re-read would still say 2 and fail the assertions below.
    let available = 2;
    vi.mocked(adminApi.user).mockImplementation(async (id: string) => ({
      ...detailOf(account(id)),
      cards: { available, expired: 0, used: 0, total: 2, cards: [] },
    }));
    rowOf('a').click();
    await settle();
    vi.spyOn(adminApi, 'rescheduleCards').mockImplementation(async () => {
      available = 1;
      return { moved: 2 };
    });

    buttonNamed(t('rescheduleCards', { count: 2 })).click();
    await settle();

    expect(panels.textContent).toContain(t('cardsMoved', { count: 2 }));
    expect(buttonNamed(t('rescheduleCards', { count: 1 }))).toBeDefined();
    expect(buttonNamed(t('rescheduleCards', { count: 2 }))).toBeUndefined();
  });

  // A list that failed to come back is not a list still loading: the loading
  // line under the error would say the opposite of what happened.
  it('does not keep the loading line under the error when the conversations fail', async () => {
    await mount([account('a')], '');
    rowOf('a').click();
    await settle();
    vi.spyOn(adminApi, 'userConversations').mockRejectedValue(new Error('conversations unavailable'));

    buttonNamed(t('viewConversations')).click();
    await settle();

    expect(panels.textContent).toContain('conversations unavailable');
    expect(panels.textContent).not.toContain(t('loading'));
  });

  // A keys request that failed is not an account without keys: the panel must
  // say the keys could not be read, where the empty line would say there are none.
  it('says the keys could not be read, rather than that there are none', async () => {
    await mount([account('a')], '');
    vi.mocked(adminApi.userKeys).mockRejectedValue(new Error('keys unavailable'));

    rowOf('a').click();
    await settle();

    expect(panels.textContent).toContain('keys unavailable');
    expect(panels.textContent).not.toContain(t('keysEmpty'));
  });

  // The keys need only the id, so with the row already listed they are asked for
  // while the detail is still on its way. The panel can match the answer to the
  // account only because the list supplied it.
  it('asks for the keys while the detail is still loading, when the list has the row', async () => {
    await mount([account('a')], '');
    const detail = deferred<Detail>();
    vi.mocked(adminApi.user).mockImplementation(() => detail.promise);

    rowOf('a').click();
    await settle();

    expect(adminApi.userKeys).toHaveBeenCalledWith('a');
    detail.resolve(detailOf(account('a')));
    await settle();
    expect(panels.textContent).toContain(t('keysEmpty'));
  });

  // Once the panel has moved on, a card change that lands must not read the
  // account it left again: no card list on screen can show the answer.
  it('does not re-read an account the panel has left when its card grant lands', async () => {
    await mount([account('a'), account('b')], '');
    rowOf('a').click();
    await settle();
    const grant = deferred<void>();
    vi.spyOn(adminApi, 'grantCards').mockImplementation(() => grant.promise);

    buttonNamed(t('grantCards')).click();
    await settle();
    rowOf('b').click();
    await settle();
    vi.mocked(adminApi.user).mockClear();
    grant.resolve();
    await settle();

    expect(adminApi.user).not.toHaveBeenCalledWith('a');
  });

  // The group names only label the rows. When they fail, the accounts still
  // load and the failure is reported above them rather than in place of them.
  it('still lists the accounts when the group names fail to load', async () => {
    await mount([account('a')], '', true);

    expect(rowOf('a')).toBeDefined();
    expect(host.textContent).toContain('names unavailable');
  });
});
