import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import { createApp, h, nextTick, shallowRef, type App } from 'vue';
import { createMemoryHistory, createRouter } from 'vue-router';
import { adminApi, type Account } from '../src/admin/api';
import { providePanelHost } from '../src/composables/usePanelHost';
import { t } from '../src/composables/useI18n';
import AdminUsers from '../src/views/admin/AdminUsers.vue';
import { provideAdminView } from '../src/views/admin/adminView';

const settle = async () => {
  await new Promise((resolve) => setTimeout(resolve, 0));
  await nextTick();
};

const account = (id: string, over: Partial<Account> = {}): Account => ({
  id, username: `user-${id}`, email: '', nickname: '', avatar: '', bio: '',
  role: 'user', group_id: '', group_expires_at: 0, group_name: '',
  status: 'active', ban_reason: '', created_at: 1, updated_at: 1, last_login_at: 0,
  email_verified: true, allow_stats: true, allow_delete_conversations: true,
  api_restricted: false, api_restricted_until: 0, api_restriction_source: '',
  ...over,
}) as Account;

type Detail = Awaited<ReturnType<typeof adminApi.user>>;
// Only what the panel reads; the figures themselves are not what is under test.
const detailOf = (row: Account): Detail => ({
  user: row,
  usage: { windows: [], unlimited: true } as unknown as Detail['usage'],
  lifetime: { requests: 0, total_tokens: 0, credits: 0 } as unknown as Detail['lifetime'],
  models: [],
  policy: {} as Detail['policy'],
  cards: { available: 0, expired: 0, used: 0, total: 0, cards: [] } as unknown as Detail['cards'],
});

describe('AdminUsers, banning with a reason', () => {
  let app: App | undefined;
  let host: HTMLElement;
  let panels: HTMLElement;
  let catalogue: Account[];

  // The one request every ban, unban and save goes through, as the page sends it.
  const updates = () => vi.mocked(adminApi.updateUser).mock.calls;

  async function mount(rows: Account[]) {
    catalogue = rows;
    vi.spyOn(adminApi, 'groupOptions').mockResolvedValue({ groups: [] });
    vi.spyOn(adminApi, 'users').mockImplementation(async () => ({ users: catalogue, total: catalogue.length }));
    vi.spyOn(adminApi, 'user').mockImplementation(async (id: string) => detailOf(catalogue.find((row) => row.id === id)!));
    vi.spyOn(adminApi, 'userKeys').mockResolvedValue({ keys: [] });
    vi.spyOn(adminApi, 'userSessions').mockResolvedValue({ sessions: [] });
    vi.spyOn(adminApi, 'updateUser').mockImplementation(async (id, patch) => {
      const row = catalogue.find((entry) => entry.id === id)!;
      const next = { ...row, ...(patch as Partial<Account>) };
      catalogue = catalogue.map((entry) => (entry.id === id ? next : entry));
      return { user: next };
    });

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
  }

  const rowBox = (index: number) => host.querySelectorAll<HTMLInputElement>('tbody input[type="checkbox"]')[index]!;
  // The list shows each account's username as "@name", which is what tells rows apart.
  const rowOf = (id: string) => [...host.querySelectorAll<HTMLTableRowElement>('tbody tr')]
    .find((row) => row.textContent?.includes(`@user-${id}`))!;
  const barButton = (label: string) => [...host.querySelectorAll<HTMLButtonElement>('.oa-bulk-bar button')]
    .find((button) => button.textContent?.trim() === label)!;
  const statusCard = () => panels.querySelector<HTMLElement>('#secBanStatus')!;
  const cardButton = (label: string) => [...statusCard().querySelectorAll<HTMLButtonElement>('button')]
    .find((button) => button.textContent?.trim() === label);
  const typeInto = (input: HTMLInputElement, value: string) => {
    input.value = value;
    input.dispatchEvent(new Event('input'));
  };

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

  it('bans one account with its reason in one request, on a second click', async () => {
    await mount([account('a'), account('b')]);
    rowOf('a').click();
    await settle();

    typeInto(statusCard().querySelector<HTMLInputElement>('input')!, '  Abuse of resources  ');
    await nextTick();
    cardButton(t('banAccount'))!.click();
    await nextTick();
    // The first click only arms it; nothing has been sent yet.
    expect(updates()).toHaveLength(0);
    statusCard().querySelector<HTMLButtonElement>('.oa-btn.armed')!.click();
    await settle();

    expect(updates()).toEqual([['a', { status: 'disabled', ban_reason: 'Abuse of resources' }]]);
    // The panel and the list both show it as banned, with the reason and an unban.
    expect(statusCard().textContent).toContain(t('statusDisabled'));
    expect(statusCard().textContent).toContain(t('banReasonLine', { reason: 'Abuse of resources' }));
    expect(cardButton(t('unbanAccount'))).toBeDefined();
    expect(rowOf('a').textContent).toContain(t('statusDisabled'));
  });

  it('unbans with status active alone, and shows the reason of a banned account', async () => {
    await mount([account('b', { status: 'disabled', ban_reason: 'Spam' })]);
    rowOf('b').click();
    await settle();

    expect(statusCard().textContent).toContain(t('banReasonLine', { reason: 'Spam' }));
    cardButton(t('unbanAccount'))!.click();
    await settle();

    expect(updates()).toEqual([['b', { status: 'active' }]]);
    // Back to an active account: the ban form is offered again, with no reason kept.
    expect(cardButton(t('banAccount'))).toBeDefined();
    expect(statusCard().querySelector<HTMLInputElement>('input')!.value).toBe('');
  });

  it('does not carry a reason typed for one account onto the next one opened', async () => {
    await mount([account('a'), account('c')]);
    rowOf('a').click();
    await settle();
    typeInto(statusCard().querySelector<HTMLInputElement>('input')!, 'for a only');
    await nextTick();

    rowOf('c').click();
    await settle();
    expect(statusCard().querySelector<HTMLInputElement>('input')!.value).toBe('');
  });

  it('saves the profile without sending the ban state, so a save cannot ban or unban', async () => {
    await mount([account('a')]);
    rowOf('a').click();
    await settle();

    panels.querySelector<HTMLButtonElement>('.oa-panel-foot .oa-btn.primary')!.click();
    await settle();

    expect(updates()).toHaveLength(1);
    const [, patch] = updates()[0]!;
    expect(patch).not.toHaveProperty('status');
    expect(patch).not.toHaveProperty('ban_reason');
  });

  it('bans every ticked account with the one reason, and leaves the one already banned for it alone', async () => {
    await mount([
      account('a'),
      account('b', { status: 'disabled', ban_reason: 'Spam' }),
      account('c'),
    ]);
    rowBox(0).click();
    await nextTick();
    rowBox(1).click();
    await nextTick();
    rowBox(2).click();
    await nextTick();

    typeInto(host.querySelector<HTMLInputElement>('.oa-bulk-bar input.oa-bulk-reason')!, 'Spam');
    await nextTick();
    barButton(t('bulkBan'))!.click();
    await nextTick();
    // Armed, not sent: the second click on the same button is the one that acts.
    expect(updates()).toHaveLength(0);
    host.querySelector<HTMLButtonElement>('.oa-bulk-bar .oa-btn.armed')!.click();
    await settle();

    expect(updates()).toEqual([
      ['a', { status: 'disabled', ban_reason: 'Spam' }],
      ['c', { status: 'disabled', ban_reason: 'Spam' }],
    ]);
    // Nothing is left ticked, so the bar is gone; the next selection starts with
    // the reason field empty rather than carrying this one forward.
    expect(host.querySelector('.oa-bulk-bar')).toBeNull();
    rowBox(0).click();
    await nextTick();
    expect(host.querySelector<HTMLInputElement>('.oa-bulk-bar input.oa-bulk-reason')!.value).toBe('');
  });

  it('bans a ticked account that was banned for another reason, with the new one', async () => {
    await mount([account('b', { status: 'disabled', ban_reason: 'Old' })]);
    rowBox(0).click();
    await nextTick();
    typeInto(host.querySelector<HTMLInputElement>('.oa-bulk-bar input.oa-bulk-reason')!, 'New');
    await nextTick();
    barButton(t('bulkBan'))!.click();
    await nextTick();
    host.querySelector<HTMLButtonElement>('.oa-bulk-bar .oa-btn.armed')!.click();
    await settle();

    expect(updates()).toEqual([['b', { status: 'disabled', ban_reason: 'New' }]]);
  });

  it('unbans every ticked account with status active, and leaves the active ones alone', async () => {
    await mount([
      account('a'),
      account('b', { status: 'disabled', ban_reason: 'Spam' }),
      account('c', { status: 'disabled', ban_reason: 'Other' }),
    ]);
    rowBox(0).click();
    await nextTick();
    rowBox(1).click();
    await nextTick();
    rowBox(2).click();
    await nextTick();

    barButton(t('unbanAccount'))!.click();
    await settle();

    // `a` is active already, so it costs no request.
    expect(updates()).toEqual([
      ['b', { status: 'active' }],
      ['c', { status: 'active' }],
    ]);
    expect(rowOf('b').textContent).not.toContain(t('statusDisabled'));
  });
});
