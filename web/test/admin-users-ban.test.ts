import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import { createApp, h, nextTick, shallowRef, type App } from 'vue';
import { createMemoryHistory, createRouter } from 'vue-router';
import { adminApi, type Account } from '../src/admin/api';
import { providePanelHost } from '../src/composables/usePanelHost';
import { currentUser, site, siteInfo } from '../src/stores/session';
import { installPlugins } from '../src/plugins/registry';
import { t } from '../src/composables/useI18n';
import AdminUsers from '../src/views/admin/AdminUsers.vue';
import { provideAdminView } from '../src/views/admin/adminView';
import example from './fixtures/example.plugin';
import { PAST_SEARCH_DEBOUNCE_MS, settle, wait } from './helpers';
import { makeAccount } from './fixtures/account';

const deferred = <T,>() => {
  let resolve!: (value: T) => void;
  const promise = new Promise<T>((done) => { resolve = done; });
  return { promise, resolve };
};

;

const account = (id: string, over: Partial<Account> = {}): Account => makeAccount({
  id, username: `user-${id}`, email: '', nickname: '',
  group_id: '', group_name: '', ban_reason: '',
  ...over,
});

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
    currentUser.value = null;
    installPlugins([]);
    site.value = null;
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
  const footerSave = () => panels.querySelector<HTMLButtonElement>('.oa-panel-foot .oa-btn.primary');
  // The field of the account panel with this label, as a reader would find it.
  const fieldFor = (label: string) => [...panels.querySelectorAll<HTMLElement>('.oa-field')]
    .find((node) => node.querySelector('.oa-field-label')?.textContent?.trim() === label)!
    .querySelector<HTMLInputElement>('input')!;

  // A save that is still in flight when another account is opened writes what
  // it was sent for, to the account it was sent for. The form it used to read
  // belonged to the account opened last.
  it('saves the account a save started with when another is opened during the save', async () => {
    currentUser.value = account('root', { role: 'super_admin' });
    await mount([account('a'), account('b')]);
    const policy = vi.spyOn(adminApi, 'savePolicy').mockResolvedValue({} as never);
    const update = deferred<{ user: Account }>();
    vi.mocked(adminApi.updateUser).mockImplementationOnce(() => update.promise);

    rowOf('a').click();
    await settle();
    typeInto(fieldFor(t('nickname')), 'Alpha');
    typeInto(fieldFor(t('requestsPerMinute')), '50');
    await nextTick();
    footerSave()!.click();
    await nextTick();
    rowOf('b').click();
    await settle();
    update.resolve({ user: { ...account('a'), nickname: 'Alpha' } });
    await settle();

    expect(updates()[0]![0]).toBe('a');
    expect(policy).toHaveBeenCalledTimes(1);
    expect(policy.mock.calls[0]![0]).toMatchObject({ scope_id: 'a', rpm: 50 });
    // The panel stays on the account opened last, and offers its own Save.
    expect(footerSave()).not.toBeNull();
  });

  // A panel left open while another administrator edits the account must not
  // write its older copy of the profile back: only what was typed is sent, and
  // the limits only when they changed.
  it('sends only the fields the operator changed', async () => {
    currentUser.value = account('root', { role: 'super_admin' });
    await mount([account('a', { nickname: 'Old', bio: 'Kept' })]);
    const policy = vi.spyOn(adminApi, 'savePolicy').mockResolvedValue({} as never);
    rowOf('a').click();
    await settle();
    typeInto(fieldFor(t('nickname')), 'Alpha');
    await nextTick();

    footerSave()!.click();
    await settle();

    expect(updates()).toEqual([['a', { nickname: 'Alpha' }]]);
    expect(policy).not.toHaveBeenCalled();
  });

  // The same holds one level down: a plugin field another administrator changed
  // is not written back by a panel that only edited a different one.
  it('sends only the plugin field the operator changed', async () => {
    installPlugins([{ name: 'ops', fields: { ref: example.fields!['ref']!, tag: example.fields!['ref']! } }]);
    site.value = { ...siteInfo.value, fields: { ref: 'optional', tag: 'optional' } };
    currentUser.value = account('root', { role: 'super_admin' });
    await mount([account('a', { fields: { ref: '10001', tag: '20002' } })]);
    rowOf('a').click();
    await settle();

    typeInto(panels.querySelector<HTMLInputElement>('input[name="field-tag"]')!, '30003');
    await nextTick();
    footerSave()!.click();
    await settle();

    expect(updates()).toEqual([['a', { fields: { tag: '30003' } }]]);
  });

  // A save that fails after the profile went through says so, and a second
  // Save writes what is still missing without sending the profile again.
  it('reports a half-finished save as partly saved, and retries only what is left', async () => {
    currentUser.value = account('root', { role: 'super_admin' });
    await mount([account('a')]);
    const policy = vi.spyOn(adminApi, 'savePolicy')
      .mockRejectedValueOnce(new Error('policy store down'))
      .mockResolvedValue({} as never);
    rowOf('a').click();
    await settle();
    typeInto(fieldFor(t('nickname')), 'Alpha');
    typeInto(fieldFor(t('requestsPerMinute')), '50');
    await nextTick();

    footerSave()!.click();
    await settle();
    // A plain Error is shown as String() makes it, which is what the panel shows.
    expect(panels.textContent).toContain(t('savedBeforeError', { error: 'Error: policy store down' }));
    expect(updates()).toHaveLength(1);

    footerSave()!.click();
    await settle();
    // The retry's profile request is empty: the name is not sent a second time.
    expect(updates()).toHaveLength(2);
    expect(updates()[1]![1]).toEqual({});
    expect(policy).toHaveBeenCalledTimes(2);
    expect(policy.mock.calls[1]![0]).toMatchObject({ scope_id: 'a', rpm: 50 });
  });

  // The two-step reset does not block the list, so the account it was sent for
  // can be left while it is in flight. Its answer must not change the panel.
  it('does not put a two-step reset of one account into the panel of another', async () => {
    await mount([account('a', { two_factor_at: 1 }), account('b', { nickname: 'Bee' })]);
    rowOf('a').click();
    await settle();
    const reset = deferred<{ user: Account }>();
    vi.spyOn(adminApi, 'resetTwoFactor').mockImplementation(() => reset.promise);
    const resetButton = [...panels.querySelectorAll<HTMLButtonElement>('button')]
      .find((button) => button.textContent?.trim() === t('twoFactorResetLabel'))!;
    resetButton.click();
    await nextTick();
    resetButton.click();
    await nextTick();

    rowOf('b').click();
    await settle();
    reset.resolve({ user: account('a', { two_factor_at: 0 }) });
    await settle();

    expect(panels.textContent).not.toContain(t('twoFactorResetDone'));
    footerSave()!.click();
    await settle();
    expect(updates().at(-1)![0]).toBe('b');
  });

  // Nothing about the list needs the page any more, so a search that was
  // waiting out its pause when the page was left must not run.
  it('does not run a search that was typed just before the page was left', async () => {
    await mount([account('a')]);
    const searches = () => vi.mocked(adminApi.users).mock.calls.length;
    const before = searches();
    typeInto(host.querySelector<HTMLInputElement>('input[type="search"]')!, 'alp');
    await nextTick();

    app!.unmount();
    app = undefined;
    await wait(PAST_SEARCH_DEBOUNCE_MS);
    expect(searches()).toBe(before);
  });
});
