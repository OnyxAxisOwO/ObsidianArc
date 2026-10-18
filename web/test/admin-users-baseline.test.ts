// The account panel's Save diffs the form against the account as it was loaded
// or last saved, not against the figures a plugin action re-read. A failure
// that comes back after the operator has opened another account is written to
// the list under the account's name, and a save that wrote nothing before a
// failure does not say that it did.

import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import { nextTick } from 'vue';
import { adminApi, type Account } from '../src/admin/api';
import { safeModeEnabled } from '../src/admin/safeMode';
import { ApiError } from '../src/api/client';
import { t } from '../src/composables/useI18n';
import { installPlugins } from '../src/plugins/registry';
import AdminUsers from '../src/views/admin/AdminUsers.vue';
import { adminShell, catchAllRouter, mountComponent, settle } from './helpers';
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
  let reject!: (reason: unknown) => void;
  const promise = new Promise<T>((done, fail) => { resolve = done; reject = fail; });
  return { promise, resolve, reject };
};

describe('AdminUsers, the account panel after a change', () => {
  let unmount: (() => void) | undefined;
  let host: HTMLElement;
  let panels: HTMLElement;

  async function mount(rows: Account[]): Promise<void> {
    vi.spyOn(adminApi, 'groupOptions').mockResolvedValue({ groups: [] });
    vi.spyOn(adminApi, 'users').mockResolvedValue({ users: rows, total: rows.length });
    vi.spyOn(adminApi, 'user').mockImplementation(async (id: string) => detailOf(rows.find((row) => row.id === id)!));
    vi.spyOn(adminApi, 'userKeys').mockResolvedValue({ keys: [] });
    vi.spyOn(adminApi, 'userSessions').mockResolvedValue({ sessions: [] });
    vi.spyOn(adminApi, 'updateUser').mockImplementation(async () => ({ user: rows[0]! }));

    unmount = await mountComponent(AdminUsers, {}, {
      host, panels, adminView: adminShell(), router: await catchAllRouter(),
    });
  }

  const rowOf = (id: string) => [...host.querySelectorAll<HTMLTableRowElement>('tbody tr')]
    .find((row) => row.textContent?.includes(`@user-${id}`))!;
  const footerSave = () => panels.querySelector<HTMLButtonElement>('.oa-panel-foot .oa-btn.primary')!;
  const fieldFor = (label: string) => [...panels.querySelectorAll<HTMLElement>('.oa-field')]
    .find((node) => node.querySelector('.oa-field-label')?.textContent?.trim() === label)!
    .querySelector<HTMLInputElement>('input')!;
  const typeIn = (field: HTMLInputElement, value: string) => {
    field.value = value;
    field.dispatchEvent(new Event('input', { bubbles: true }));
  };
  const buttonNamed = (label: string) => [...panels.querySelectorAll<HTMLButtonElement>('button')]
    .find((button) => button.textContent?.trim() === label)!;

  beforeEach(() => {
    safeModeEnabled.value = false;
    host = document.createElement('div');
    panels = document.createElement('div');
    document.body.append(host, panels);
  });

  afterEach(() => {
    unmount?.();
    unmount = undefined;
    host.remove();
    panels.remove();
    installPlugins([]);
    safeModeEnabled.value = false;
    vi.restoreAllMocks();
  });

  // Opening B while A's save is in flight: A's failure is not in B's panel, so
  // it has to reach the list under A's name.
  it('reports a failed save for the account it was sent for once another account is open', async () => {
    await mount([account('a'), account('b')]);
    rowOf('a').click();
    await settle();
    const save = deferred<{ user: Account }>();
    vi.mocked(adminApi.updateUser).mockImplementation(() => save.promise);
    typeIn(fieldFor(t('nickname')), 'Changed');
    await nextTick();

    footerSave().click();
    await nextTick();
    rowOf('b').click();
    await settle();
    save.reject(new ApiError(409, 'nickname_taken', 'nickname taken'));
    await settle();

    expect(host.textContent).toContain(t('accountChangeFailed', { name: 'Nick a', error: 'nickname taken' }));
    expect(panels.textContent).not.toContain('nickname taken');
  });

  // A plugin action re-reads the account, and another administrator may have
  // changed it. The readouts show that; Save must not send this form's older
  // nickname back over the change.
  it('shows the account as it is after a plugin action, and saves only what the operator changed', async () => {
    installPlugins([{ name: 'ops', userActions: [{
      id: 'quota',
      title: () => 'Quota',
      buttons: [{ label: () => 'Reset quota', confirm: () => 'Reset quota?', run: async () => 'Quota reset' }],
    }] }]);
    await mount([account('a')]);
    rowOf('a').click();
    await settle();
    vi.mocked(adminApi.user).mockImplementation(async () => detailOf(account('a', {
      nickname: 'Someone else', status: 'disabled', ban_reason: 'spam',
    })));

    const action = buttonNamed('Reset quota');
    action.click();
    await nextTick();
    action.click();
    await settle();

    expect(panels.textContent).toContain('Quota reset');
    expect(panels.textContent).toContain(t('banReasonLine', { reason: 'spam' }));

    footerSave().click();
    await settle();
    const sent = vi.mocked(adminApi.updateUser).mock.calls[0]?.[1] as Record<string, unknown>;
    expect(sent).not.toHaveProperty('nickname');
  });

  // The profile was saved as an empty patch, which writes nothing, so a password
  // that then fails is not a save that half went through.
  it('does not say a save was partly kept when only the password failed', async () => {
    await mount([account('a')]);
    rowOf('a').click();
    await settle();
    vi.spyOn(adminApi, 'resetPassword').mockRejectedValue(new ApiError(400, 'weak', 'too weak'));
    typeIn(fieldFor(t('setNewPassword')), 'short');
    await nextTick();

    footerSave().click();
    await settle();

    expect(panels.textContent).toContain('too weak');
    expect(panels.textContent).not.toContain(t('savedBeforeError', { error: 'too weak' }));
  });
});
