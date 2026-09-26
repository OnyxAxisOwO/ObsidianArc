import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import { createApp, h, nextTick, shallowRef, type App, type Component } from 'vue';
import AdminCodes from '../src/views/admin/AdminCodes.vue';
import AdminUsers from '../src/views/admin/AdminUsers.vue';
import GroupMembers from '../src/views/admin/GroupMembers.vue';
import { adminApi, emptyPolicy, type Group, type RedemptionCode } from '../src/admin/api';
import type { Account } from '../src/api/auth';
import { saveAsFile } from '../src/api/backup';
import { provideAdminView } from '../src/views/admin/adminView';
import { providePanelHost } from '../src/composables/usePanelHost';
import { changeLanguage, t } from '../src/composables/useI18n';
import { adopt, forget } from '../src/stores/session';

vi.mock('../src/api/backup', () => ({ saveAsFile: vi.fn() }));

let app: App | undefined;
let host: HTMLElement;
let panels: HTMLElement;
let actions: HTMLElement;

beforeEach(async () => {
  await changeLanguage('en');
  host = document.createElement('div');
  panels = document.createElement('div');
  actions = document.createElement('div');
  document.body.append(host, panels, actions);
});

afterEach(() => {
  app?.unmount();
  app = undefined;
  document.body.textContent = '';
  vi.restoreAllMocks();
  forget();
  vi.mocked(saveAsFile).mockClear();
});

async function settle(): Promise<void> {
  await new Promise((resolve) => setTimeout(resolve, 0));
  await nextTick();
}

async function mount(component: Component, props = {}): Promise<void> {
  app = createApp({ setup() {
    providePanelHost(shallowRef(panels));
    provideAdminView({ actionsHost: actions, setTitle: () => {}, reload: () => {}, params: [] });
    return () => h(component, props);
  } });
  app.mount(host);
  await settle();
}

function input(node: HTMLInputElement, value: string): void {
  node.value = value;
  node.dispatchEvent(new Event('input', { bubbles: true }));
}

function button(root: ParentNode, label: string): HTMLButtonElement {
  const found = [...root.querySelectorAll<HTMLButtonElement>('button')].find((node) => node.textContent?.trim() === label);
  if (!found) throw new Error(`Button not found: ${label}`);
  return found;
}

function code(value: string, at: string): RedemptionCode {
  return { id: value, code: value, cards: 1, claimed: 0, card_days: 10, expires_at: 0, note: '', created_at: new Date(at).getTime() };
}

describe('administrator roles on the user detail panel', () => {
  const member: Account = {
    id: 'member', username: 'member', nickname: 'Member', email: '', qq: '', bio: '', avatar: '',
    role: 'user', status: 'active', group_id: '', group_expires_at: 0, admin_permissions: [],
    created_at: Date.now(), updated_at: Date.now(), last_login_at: 0, group_name: '',
    email_verified: true, allow_stats: true, allow_delete_conversations: true,
    api_restricted: false, api_restricted_until: 0, api_restriction_source: '',
  };

  async function openMember(permissions: string[]): Promise<void> {
    adopt({ ...member, id: 'operator', role: 'admin', admin_permissions: permissions });
    const policy = emptyPolicy('user', member.id);
    vi.spyOn(adminApi, 'groupOptions').mockResolvedValue({ groups: [] });
    vi.spyOn(adminApi, 'users').mockResolvedValue({ users: [member], total: 1 });
    vi.spyOn(adminApi, 'user').mockResolvedValue({
      user: member, policy, usage: { windows: [], unlimited: true },
      lifetime: { requests: 0, input_tokens: 0, output_tokens: 0, reasoning_tokens: 0, total_tokens: 0, credits: 0, errors: 0, users: 0, models: 0, duration_ms: 0 },
      cards: { total: 0, available: 0, used: 0, expired: 0, cards: [] },
    });
    vi.spyOn(adminApi, 'userKeys').mockResolvedValue({ keys: [] });
    vi.spyOn(adminApi, 'savePolicy').mockResolvedValue({ policy });
    await mount(AdminUsers);
    host.querySelector<HTMLTableRowElement>('tbody tr')!.click();
    await settle();
  }

  function switchNamed(label: string): HTMLInputElement {
    const field = [...panels.querySelectorAll<HTMLLabelElement>('.oa-checkbox-field')].find((node) =>
      node.querySelector('span')?.textContent === label);
    if (!field) throw new Error(`Switch not found: ${label}`);
    return field.querySelector<HTMLInputElement>('input[type="checkbox"]')!;
  }

  function roleField(): HTMLElement | undefined {
    return [...panels.querySelectorAll<HTMLElement>('.oa-field')].find((field) =>
      field.querySelector('.oa-field-label')?.textContent === t('role'));
  }

  it('edits ordinary profile fields without exposing or submitting role controls when the action grant is absent', async () => {
    await openMember(['users']);
    const update = vi.spyOn(adminApi, 'updateUser').mockResolvedValue({ user: member });
    expect(roleField()).toBeUndefined();
    expect(panels.textContent).not.toContain(t('manageAdministrators'));
    button(panels, t('save')).click();
    await settle();
    expect(update).toHaveBeenCalledOnce();
    expect(update.mock.calls[0]![1]).not.toHaveProperty('role');
    expect(update.mock.calls[0]![1]).not.toHaveProperty('admin_permissions');
  });

  // The switch is a shortcut over fields the policy model already has, so
  // what matters is the policy it actually saves: a rate limit of 0 is "no
  // rate limit", and a window disabled at the user level is an exemption that
  // beats whatever the group says.
  it('exempts one account from every limit with a single switch', async () => {
    await openMember(['users', 'usage']);
    const save = vi.spyOn(adminApi, 'savePolicy').mockResolvedValue({ policy: emptyPolicy('user', member.id) });
    vi.spyOn(adminApi, 'updateUser').mockResolvedValue({ user: member });

    switchNamed(t('unlimitedQuota')).click();
    await settle();
    button(panels, t('save')).click();
    await settle();

    expect(save).toHaveBeenCalledWith(expect.objectContaining({
      scope: 'user', scope_id: member.id, rpm: 0, tpm: 0,
      windows: {
        '5h': { enabled: false, requests: null, tokens: null, credits: null },
        '1w': { enabled: false, requests: null, tokens: null, credits: null },
        '1m': { enabled: false, requests: null, tokens: null, credits: null },
      },
    }));
  });

  // Off is not "remember what it was before": the account goes back to being
  // an ordinary member of its group, which is the only other answer that
  // means anything.
  it('returns every limit to the group when the switch goes off again', async () => {
    await openMember(['users', 'usage']);
    const save = vi.spyOn(adminApi, 'savePolicy').mockResolvedValue({ policy: emptyPolicy('user', member.id) });
    vi.spyOn(adminApi, 'updateUser').mockResolvedValue({ user: member });

    const toggle = switchNamed(t('unlimitedQuota'));
    toggle.click();
    await settle();
    expect(toggle.checked).toBe(true);
    toggle.click();
    await settle();
    expect(toggle.checked).toBe(false);

    button(panels, t('save')).click();
    await settle();
    expect(save).toHaveBeenCalledWith(expect.objectContaining({
      rpm: null, tpm: null,
      windows: {
        '5h': { enabled: null, requests: null, tokens: null, credits: null },
        '1w': { enabled: null, requests: null, tokens: null, credits: null },
        '1m': { enabled: null, requests: null, tokens: null, credits: null },
      },
    }));
  });

  it('appoints an administrator and selects permissions in the same user detail panel', async () => {
    await openMember(['users', 'administrators']);
    const update = vi.spyOn(adminApi, 'updateUser').mockResolvedValue({ user: { ...member, role: 'admin' } });
    roleField()!.querySelector<HTMLButtonElement>('.oa-select')!.click();
    await settle();
    const option = [...document.querySelectorAll<HTMLElement>('[role="option"]')].find((item) => item.textContent?.trim() === t('roleAdmin'))!;
    option.click(); await settle();
    const grants = [...panels.querySelectorAll<HTMLLabelElement>('.oa-check-row')];
    expect(grants.map((row) => row.querySelector('.oa-check-title')?.textContent)).toEqual([t('navUsers'), t('manageAdministrators')]);
    grants[0]!.querySelector<HTMLInputElement>('input')!.click();
    await settle();
    button(panels, t('save')).click();
    await settle();
    expect(update).toHaveBeenCalledWith(member.id, expect.objectContaining({ role: 'admin', admin_permissions: ['users'] }));
  });
  // A heavy account takes seconds to total. The click has to show that it
  // was heard, and a failure has to land where the reader is looking, not
  // above a list they have scrolled away from.
  it('opens the panel on the click and reports a failed detail inside it', async () => {
    adopt({ ...member, id: 'operator', role: 'super_admin', admin_permissions: [] });
    vi.spyOn(adminApi, 'groupOptions').mockResolvedValue({ groups: [] });
    vi.spyOn(adminApi, 'users').mockResolvedValue({ users: [member], total: 1 });
    vi.spyOn(adminApi, 'userKeys').mockResolvedValue({ keys: [] });
    let fail: (reason: Error) => void = () => {};
    vi.spyOn(adminApi, 'user').mockReturnValue(new Promise((_, reject) => { fail = reject; }));
    await mount(AdminUsers);

    host.querySelector<HTMLTableRowElement>('tbody tr')!.click();
    await settle();
    expect(panels.querySelector('.oa-panel')).not.toBeNull();
    expect(panels.textContent).toContain(t('loading'));
    expect(button(panels, '…').disabled).toBe(true);

    fail(new Error('Database is busy'));
    await settle();
    expect(panels.textContent).toContain('Database is busy');
    expect(panels.textContent).not.toContain(t('loading'));
  });

});

// Granting and moving share one date field and sit next to each other, which
// is the whole risk: an operator who meant "their card lasts longer" must not
// be able to mint a new one by pressing the wrong button, and the two must
// not be able to disagree about which date they read.
describe('moving the expiry on cards an account already holds', () => {
  const holder: Account = {
    id: 'holder', username: 'holder', nickname: 'Holder', email: '', qq: '', bio: '', avatar: '',
    role: 'user', status: 'active', group_id: '', group_expires_at: 0, admin_permissions: [],
    created_at: Date.now(), updated_at: Date.now(), last_login_at: 0, group_name: '',
    email_verified: true, allow_stats: true, allow_delete_conversations: true,
    api_restricted: false, api_restricted_until: 0, api_restriction_source: '',
  };

  // Two spendable, one already lapsed — the lapsed one is invisible in the
  // list and still has to be counted by the button that would move it.
  const holding = {
    total: 3, available: 2, used: 0, expired: 1,
    cards: [
      { id: 'card-a', source: 'grant' as const, expires_at: Date.now() + 86_400_000, used_at: 0, created_at: 0 },
      { id: 'card-b', source: 'code' as const, expires_at: Date.now() + 172_800_000, used_at: 0, created_at: 0 },
    ],
  };

  async function openHolder(): Promise<void> {
    adopt({ ...holder, id: 'operator', role: 'admin', admin_permissions: ['users'] });
    const policy = emptyPolicy('user', holder.id);
    vi.spyOn(adminApi, 'groupOptions').mockResolvedValue({ groups: [] });
    vi.spyOn(adminApi, 'users').mockResolvedValue({ users: [holder], total: 1 });
    vi.spyOn(adminApi, 'user').mockResolvedValue({
      user: holder, policy, usage: { windows: [], unlimited: true },
      lifetime: { requests: 0, input_tokens: 0, output_tokens: 0, reasoning_tokens: 0, total_tokens: 0, credits: 0, errors: 0, users: 0, models: 0, duration_ms: 0 },
      cards: holding,
    });
    vi.spyOn(adminApi, 'userKeys').mockResolvedValue({ keys: [] });
    await mount(AdminUsers);
    host.querySelector<HTMLTableRowElement>('tbody tr')!.click();
    await settle();
  }

  /** The shared expiry picker, set to a fixed local date and time. */
  function pickDate(value: string): void {
    const field = panels.querySelector<HTMLInputElement>('input[type="datetime-local"]')!;
    input(field, value);
  }

  it('moves every unused card, counting the expired one the list does not show', async () => {
    await openHolder();
    const move = vi.spyOn(adminApi, 'rescheduleCards').mockResolvedValue({ moved: 3 });
    const grant = vi.spyOn(adminApi, 'grantCards').mockResolvedValue(undefined);

    pickDate('2030-01-02T03:04');
    button(panels, t('rescheduleCards', { count: 3 })).click();
    await settle();

    expect(grant).not.toHaveBeenCalled();
    expect(move).toHaveBeenCalledWith(holder.id, { expires_at: new Date('2030-01-02T03:04').getTime() });
    expect(panels.textContent).toContain(t('cardsMoved', { count: 3 }));
  });

  it('moves one named card from its own row', async () => {
    await openHolder();
    const move = vi.spyOn(adminApi, 'rescheduleCards').mockResolvedValue({ moved: 1 });

    pickDate('2030-01-02T03:04');
    panels.querySelector<HTMLButtonElement>('.oa-card-row .oa-card-move')!.click();
    await settle();

    expect(move).toHaveBeenCalledWith(holder.id, {
      expires_at: new Date('2030-01-02T03:04').getTime(),
      card_ids: ['card-a'],
    });
  });

  // Two presses, not one. The first only arms it, which is the whole reason
  // this is an OaConfirmButton and not a plain button beside "Move" — the two
  // sit next to each other and one of them cannot be undone.
  it('needs a second press before it deletes a card', async () => {
    await openHolder();
    const drop = vi.spyOn(adminApi, 'revokeCard').mockResolvedValue(undefined);

    const button = panels.querySelector<HTMLButtonElement>('.oa-card-row .oa-card-drop')!;
    button.click();
    await settle();
    expect(drop).not.toHaveBeenCalled();

    button.click();
    await settle();
    expect(drop).toHaveBeenCalledWith(holder.id, 'card-a');
  });

  it('refuses a date in the past rather than sending it', async () => {
    await openHolder();
    const move = vi.spyOn(adminApi, 'rescheduleCards').mockResolvedValue({ moved: 0 });

    pickDate('2020-01-02T03:04');
    button(panels, t('rescheduleCards', { count: 3 })).click();
    await settle();

    expect(move).not.toHaveBeenCalled();
    expect(panels.textContent).toContain(t('grantCardExpiryInvalid'));
  });

  it('grants cards with a custom name and window variant', async () => {
    await openHolder();
    const grant = vi.spyOn(adminApi, 'grantCards').mockResolvedValue(undefined);

    const nameField = [...panels.querySelectorAll<HTMLElement>('.oa-field')].find((el) =>
      el.querySelector('.oa-field-label')?.textContent === t('cardName'),
    );
    expect(nameField).toBeDefined();
    input(nameField!.querySelector('input')!, 'Bonus Card');

    pickDate('2030-01-02T03:04');
    button(panels, t('grantCards')).click();
    await settle();

    expect(grant).toHaveBeenCalledWith(holder.id, {
      cards: 1,
      expires_at: new Date('2030-01-02T03:04').getTime(),
      name: 'Bonus Card',
      windows: [],
    });
  });

  it('rejects custom window scope when no checkboxes are checked', async () => {
    await openHolder();
    const grant = vi.spyOn(adminApi, 'grantCards').mockResolvedValue(undefined);

    // Select custom scope
    const selectBtn = panels.querySelector<HTMLButtonElement>('.oa-select')!;
    selectBtn.click();
    await settle();
    [...document.querySelectorAll<HTMLElement>('[role="option"]')].find((node) => node.textContent?.trim() === t('cardResetCustom'))!.click();
    await settle();

    // By default custom has '5h' checked. Uncheck it:
    const checkbox = panels.querySelector<HTMLInputElement>('.oa-check-list input[type="checkbox"]')!;
    checkbox.click();
    await settle();

    pickDate('2030-01-02T03:04');
    button(panels, t('grantCards')).click();
    await settle();

    expect(grant).not.toHaveBeenCalled();
    expect(panels.textContent).toContain(t('cardResetScopeRequired'));
  });
});

describe('redemption code creation and details', () => {
  it('shows window variant as title when code has no custom name', async () => {
    const item = { ...code('5H-CODE', '2026-09-13T12:00:00'), windows: ['5h'], name: '' };
    vi.spyOn(adminApi, 'codes').mockResolvedValue({ codes: [item] });
    vi.spyOn(adminApi, 'codeRedemptions').mockResolvedValue({ redemptions: [] });
    await mount(AdminCodes);
    await settle();

    const row = host.querySelector<HTMLElement>('.oa-table tbody tr')!;
    row.click();
    await settle();

    expect(panels.querySelector('.oa-card-title')?.textContent).toContain(t('cardScope5H'));
    expect(panels.querySelector('.oa-card-title')?.textContent).not.toContain(t('cardFullReset'));
  });

  it('rejects custom scope without windows during code creation', async () => {
    vi.spyOn(adminApi, 'codes').mockResolvedValue({ codes: [] });
    const create = vi.spyOn(adminApi, 'createCode').mockResolvedValue({ codes: [] });
    await mount(AdminCodes);
    actions.querySelector<HTMLButtonElement>('#addCode')!.click();
    await settle();

    // Select custom scope
    const selectBtn = panels.querySelector<HTMLButtonElement>('.oa-select')!;
    selectBtn.click();
    await settle();
    [...document.querySelectorAll<HTMLElement>('[role="option"]')].find((node) => node.textContent?.trim() === t('cardResetCustom'))!.click();
    await settle();

    // Uncheck 5h
    const checkbox = panels.querySelector<HTMLInputElement>('.oa-check-list input[type="checkbox"]')!;
    checkbox.click();
    await settle();

    button(panels, t('add')).click();
    await settle();

    expect(create).not.toHaveBeenCalled();
    expect(panels.textContent).toContain(t('cardResetScopeRequired'));
  });
});

describe('redemption code export', () => {
  const records = [
    code('BEFORE', '2026-09-13T11:59:59'),
    code('START', '2026-09-13T12:00:00'),
    { ...code('END', '2026-09-13T12:30:59.999'), claimed: 1, expires_at: 1 },
    code('AFTER', '2026-09-13T12:31:00'),
  ];

  it('exports creation-time matches, including the whole last minute and redeemed codes', async () => {
    vi.spyOn(adminApi, 'codes').mockResolvedValue({ codes: records });
    await mount(AdminCodes);
    actions.querySelector<HTMLButtonElement>('#exportCodes')!.click();
    await settle();
    const fields = panels.querySelectorAll<HTMLInputElement>('input[type="datetime-local"]');
    input(fields[0]!, '2026-09-13T12:00');
    input(fields[1]!, '2026-09-13T12:30');
    await settle();
    expect(panels.textContent).toContain(t('codeExportCount', { count: 2 }));
    button(panels, t('download')).click();
    expect(saveAsFile).toHaveBeenCalledWith(expect.stringMatching(/\.txt$/), 'START\nEND\n', 'text/plain;charset=utf-8');
  });

  it('supports open bounds and refuses reversed or empty ranges', async () => {
    vi.spyOn(adminApi, 'codes').mockResolvedValue({ codes: records });
    await mount(AdminCodes);
    actions.querySelector<HTMLButtonElement>('#exportCodes')!.click();
    await settle();
    expect(panels.textContent).toContain(t('codeExportCount', { count: 4 }));
    const fields = panels.querySelectorAll<HTMLInputElement>('input[type="datetime-local"]');
    input(fields[0]!, '2026-09-13T12:00');
    await settle();
    expect(panels.textContent).toContain(t('codeExportCount', { count: 3 }));
    input(fields[1]!, '2026-09-13T11:00');
    await settle();
    expect(panels.textContent).toContain(t('codeExportInvalidRange'));
    expect(panels.querySelector('.oa-panel-foot .primary')).toBeNull();
    input(fields[0]!, '');
    await settle();
    expect(panels.textContent).toContain(t('codeExportCount', { count: 0 }));
    expect(panels.querySelector('.oa-panel-foot .primary')).toBeNull();
    expect(saveAsFile).not.toHaveBeenCalled();
  });
});

const group = { id: 'premium', name: 'Premium' } as Group;
const accounts = Array.from({ length: 25 }, (_, index) => ({
  id: `user-${index}`, username: `member${index}`, nickname: '', group_id: group.id,
  group_expires_at: 0,
} as Account));

describe('group member selection', () => {
  function users(): ReturnType<typeof vi.spyOn> {
    return vi.spyOn(adminApi, 'memberOptions').mockImplementation(async (query) => {
      const params = new URLSearchParams(query);
      const offset = Number(params.get('offset'));
      return { users: accounts.slice(offset, offset + 20), total: accounts.length };
    });
  }

  it('keeps selections across pages and submits one membership update with a local expiry', async () => {
    const listing = users();
    const assign = vi.spyOn(adminApi, 'assignGroupMembers').mockResolvedValue({ updated: 2 });
    await mount(GroupMembers, { group, groups: [group] });
    expect(listing).toHaveBeenCalledWith(expect.stringContaining('group_id=premium'));
    host.querySelector<HTMLInputElement>('input[type="checkbox"]')!.click();
    button(host, t('next')).click();
    await settle();
    host.querySelector<HTMLInputElement>('input[type="checkbox"]')!.click();
    const expiry = '2099-10-11T12:30';
    input(host.querySelector<HTMLInputElement>('input[type="datetime-local"]')!, expiry);
    await settle();
    expect(host.textContent).toContain(t('membersSelected', { count: 2 }));
    button(host, t('saveMembers')).click();
    await settle();
    expect(assign).toHaveBeenCalledWith('premium', ['user-0', 'user-20'], new Date(expiry).getTime());
    expect(host.textContent).toContain(t('membersSelected', { count: 0 }));
    expect(host.textContent).toContain(t('membersSaved', { count: 2 }));
  });

  it('searches all accounts on the server, rejects past expiry and preserves a failed selection', async () => {
    const listing = users();
    const assign = vi.spyOn(adminApi, 'assignGroupMembers').mockRejectedValue(new Error('Save failed'));
    await mount(GroupMembers, { group, groups: [group] });
    host.querySelector<HTMLButtonElement>('.oa-select')!.click();
    await settle();
    [...document.querySelectorAll<HTMLElement>('[role="option"]')].find((node) => node.textContent?.trim() === t('allAccounts'))!.click();
    input(host.querySelector<HTMLInputElement>('input[type="search"]')!, 'far-away-account');
    await vi.waitFor(() => expect(listing).toHaveBeenLastCalledWith('?q=far-away-account&limit=20&offset=0'));
    await settle();
    host.querySelector<HTMLInputElement>('input[type="checkbox"]')!.click();
    input(host.querySelector<HTMLInputElement>('input[type="datetime-local"]')!, '2001-01-01T00:00');
    await settle();
    button(host, t('saveMembers')).click();
    await settle();
    expect(assign).not.toHaveBeenCalled();
    expect(host.textContent).toContain(t('membershipExpiryInvalid'));
    input(host.querySelector<HTMLInputElement>('input[type="datetime-local"]')!, '');
    await settle();
    button(host, t('saveMembers')).click();
    await settle();
    expect(assign).toHaveBeenCalledWith('premium', ['user-0'], 0);
    expect(host.textContent).toContain('Save failed');
    expect(host.querySelector<HTMLInputElement>('input[type="checkbox"]')!.checked).toBe(true);
  });
});
