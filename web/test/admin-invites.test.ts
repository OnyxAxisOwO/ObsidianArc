import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import { createApp, h, nextTick, shallowRef, type App, type Component } from 'vue';
import AdminInvites from '../src/views/admin/AdminInvites.vue';
import { adminApi, type InviteCode, type InviteStats, type InviteUse } from '../src/admin/api';
import * as authApi from '../src/api/auth';
import { provideAdminView } from '../src/views/admin/adminView';
import { providePanelHost } from '../src/composables/usePanelHost';
import { changeLanguage, t } from '../src/composables/useI18n';
import { siteInfo } from '../src/stores/session';

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
});

async function settle(): Promise<void> {
  await new Promise((resolve) => setTimeout(resolve, 0));
  await nextTick();
}

async function mount(component: Component): Promise<void> {
  app = createApp({ setup() {
    providePanelHost(shallowRef(panels));
    provideAdminView({ actionsHost: actions, setTitle: () => {}, reload: () => {}, params: [] });
    return () => h(component);
  } });
  app.mount(host);
  await settle();
}

function button(root: ParentNode, label: string): HTMLButtonElement {
  const found = [...root.querySelectorAll<HTMLButtonElement>('button')].find((node) => node.textContent?.trim() === label);
  if (!found) throw new Error(`Button not found: ${label}`);
  return found;
}

function fieldFor(root: ParentNode, label: string): HTMLElement {
  const found = [...root.querySelectorAll<HTMLElement>('.oa-field')].find((field) =>
    field.querySelector('.oa-field-label')?.textContent === label);
  if (!found) throw new Error(`Field not found: ${label}`);
  return found;
}

/** `OaSwitchField` is a `.oa-checkbox-field` label, not an `.oa-field` — it
 *  carries no separate `.oa-field-label`, so the switches need their own
 *  lookup rather than sharing `fieldFor`. */
function switchFor(root: ParentNode, label: string): HTMLInputElement {
  const found = [...root.querySelectorAll<HTMLLabelElement>('.oa-checkbox-field')].find((field) =>
    field.querySelector('span')?.textContent === label);
  if (!found) throw new Error(`Switch not found: ${label}`);
  return found.querySelector<HTMLInputElement>('input[type="checkbox"]')!;
}

/** Opens an `OaSelect` and picks the option matching `optionLabel`. */
async function choose(trigger: HTMLButtonElement, optionLabel: string): Promise<void> {
  trigger.click();
  await settle();
  const option = [...document.querySelectorAll<HTMLElement>('[role="option"]')]
    .find((node) => node.textContent?.trim() === optionLabel);
  if (!option) throw new Error(`Option not found: ${optionLabel}`);
  option.click();
  await settle();
}

function input(node: HTMLInputElement, value: string): void {
  node.value = value;
  node.dispatchEvent(new Event('input', { bubbles: true }));
}

const noStats: InviteStats = { active: 0, uses_total: 0, uses_7d: 0, top_inviters: [], partners: [] };

function stubBase(overrides: { settings?: Record<string, string> } = {}): void {
  vi.spyOn(adminApi, 'settings').mockResolvedValue({
    settings: {
      'registration.enabled': 'true',
      'invites.required': 'false',
      'invites.user_enabled': 'false',
      'invites.user_limit': '10',
      'invites.reward_every': '1',
      'invites.reward_cards': '0',
      'invites.reward_card_days': '30',
      ...overrides.settings,
    },
    groups: [],
  });
  vi.spyOn(adminApi, 'groupOptions').mockResolvedValue({ groups: [{ id: 'g1', name: 'Premium' }] });
  vi.spyOn(adminApi, 'inviteStats').mockResolvedValue(noStats);
  vi.spyOn(authApi, 'fetchSite').mockResolvedValue(siteInfo.value);
}

/** Most tests care about the main list only; the partner card fetches on its
 *  own, so it gets a quiet empty stub unless a test says otherwise. */
function stubPartners(codes: InviteCode[] = []): void {
  vi.spyOn(adminApi, 'invites').mockImplementation(async (query: string) =>
    query.includes('kind=partner') ? { codes, total: codes.length } : { codes: [], total: 0 });
}

describe('registration mode and rewards', () => {
  it('derives the mode from the two settings it stands for, and writes them back together', async () => {
    stubBase({ settings: { 'registration.enabled': 'true', 'invites.required': 'true' } });
    stubPartners();
    const save = vi.spyOn(adminApi, 'saveSettings').mockResolvedValue({ settings: {} });
    await mount(AdminInvites);

    const mode = fieldFor(host, t('registrationMode'));
    expect(mode.querySelector('.oa-select-label')?.textContent).toBe(t('registrationModeInvite'));

    await choose(mode.querySelector<HTMLButtonElement>('.oa-select')!, t('registrationModeClosed'));
    expect(actions.textContent).toContain(t('controlUnsaved'));

    button(actions, t('save')).click();
    await settle();

    expect(save).toHaveBeenCalledWith(expect.objectContaining({
      'registration.enabled': 'false',
      'invites.required': 'false',
    }));
    expect(actions.textContent).toContain(t('controlSaved'));
  });

  it('keeps the per-account limit and reward fields out of the form until personal invites are switched on', async () => {
    stubBase();
    stubPartners();
    const save = vi.spyOn(adminApi, 'saveSettings').mockResolvedValue({ settings: {} });
    await mount(AdminInvites);

    expect(host.textContent).not.toContain(t('userInviteLimit'));

    const toggle = switchFor(host, t('userInvitesEnabled'));
    toggle.click();
    await settle();
    expect(host.textContent).toContain(t('userInviteLimit'));
    expect(host.textContent).toContain(t('inviteRewardEvery'));
    expect(host.textContent).toContain(t('inviteRewardCards'));

    button(actions, t('save')).click();
    await settle();
    // The limit and reward shown are the server's own defaults, so they are not
    // written: only the switch this card changed is sent.
    expect(save).toHaveBeenCalledWith({ 'invites.user_enabled': 'true' });
  });

  it('reads the reward rule as one sentence, and explains a zero reward rather than stating it', async () => {
    stubBase({ settings: { 'invites.user_enabled': 'true', 'invites.reward_every': '5', 'invites.reward_cards': '2' } });
    stubPartners();
    await mount(AdminInvites);

    expect(host.textContent).toContain(t('inviteRewardRuleSummary', { every: 5, cards: 2, days: 30 }));

    input(fieldFor(host, t('inviteRewardCards')).querySelector<HTMLInputElement>('input')!, '0');
    await settle();
    expect(host.textContent).toContain(t('inviteRewardRuleNone', { every: 5 }));
  });
});

describe('the code list', () => {
  it('sends the kind, status and a debounced search to the server', async () => {
    stubBase();
    const list = vi.spyOn(adminApi, 'invites').mockResolvedValue({ codes: [], total: 0 });
    await mount(AdminInvites);
    expect(list).toHaveBeenCalledWith('?limit=20&offset=0');

    const [kind, status] = [...host.querySelectorAll<HTMLButtonElement>('.oa-filter-select')];
    await choose(kind!, t('inviteKindPartner'));
    expect(list).toHaveBeenLastCalledWith('?limit=20&offset=0&kind=partner');

    await choose(status!, t('inviteStatusRevoked'));
    expect(list).toHaveBeenLastCalledWith('?limit=20&offset=0&kind=partner&status=revoked');

    input(host.querySelector<HTMLInputElement>('input[type="search"]')!, 'partner');
    await vi.waitFor(() => expect(list).toHaveBeenLastCalledWith('?limit=20&offset=0&kind=partner&status=revoked&q=partner'));
  });
});

describe('generating codes', () => {
  it('only offers a name for a single code, and mints a batch otherwise', async () => {
    stubBase();
    stubPartners();
    const create = vi.spyOn(adminApi, 'createInvites').mockImplementation(async (body) => ({
      codes: Array.from({ length: Number(body['count']) }, (_, index) => ({
        id: `new-${index}`, code: `CODE${index}0000`, owner_id: '', owner_username: '', owner_nickname: '',
        kind: 'batch', name: '', allow_existing: false,
        group_id: '', group_name: '', group_days: 0, group_days_max: 0,
        max_uses: Number(body['max_uses']), uses: 0, expires_at: 0, revoked_at: 0,
        note: String(body['note']), created_by: 'op', created_at: Date.now(), status: 'active', claims: 0,
      } satisfies InviteCode)),
    }));
    await mount(AdminInvites);

    button(actions, t('generateInvites')).click();
    await settle();
    expect(panels.textContent).toContain(t('inviteCustomCode'));

    const count = fieldFor(panels, t('inviteCount')).querySelector<HTMLInputElement>('input')!;
    count.value = '5';
    count.dispatchEvent(new Event('input', { bubbles: true }));
    await settle();
    expect(panels.textContent).not.toContain(t('inviteCustomCode'));

    button(panels, t('generateInvites')).click();
    await settle();

    expect(create).toHaveBeenCalledWith(expect.objectContaining({ count: 5, code: '' }));
    expect(panels.textContent).toContain(t('codesMinted', { count: 5 }));
  });

  // The result view replaces the form the moment a batch is minted — the
  // same panel AdminCodes uses for its own codes — so each shape of the
  // range gets its own generate, rather than a second submit from a form
  // that mounting the result view has already taken off screen.
  it('leaves an untouched range as a fixed, permanent length', async () => {
    stubBase();
    stubPartners();
    const create = vi.spyOn(adminApi, 'createInvites').mockResolvedValue({ codes: [] });
    await mount(AdminInvites);

    button(actions, t('generateInvites')).click();
    await settle();
    await choose(fieldFor(panels, t('inviteGroup')).querySelector<HTMLButtonElement>('.oa-select')!, 'Premium');
    button(panels, t('generateInvites')).click();
    await settle();

    expect(create).toHaveBeenCalledWith(expect.objectContaining({
      group_id: 'g1', group_days: 0, group_days_max: 0,
    }));
  });

  it('sends a real range once "up to" is set higher than "days"', async () => {
    stubBase();
    stubPartners();
    const create = vi.spyOn(adminApi, 'createInvites').mockResolvedValue({ codes: [] });
    await mount(AdminInvites);

    button(actions, t('generateInvites')).click();
    await settle();
    await choose(fieldFor(panels, t('inviteGroup')).querySelector<HTMLButtonElement>('.oa-select')!, 'Premium');
    input(fieldFor(panels, t('inviteGroupDaysFrom')).querySelector<HTMLInputElement>('input')!, '3');
    input(fieldFor(panels, t('inviteGroupDaysTo')).querySelector<HTMLInputElement>('input')!, '7');
    await settle();
    button(panels, t('generateInvites')).click();
    await settle();

    expect(create).toHaveBeenCalledWith(expect.objectContaining({
      group_id: 'g1', group_days: 3, group_days_max: 7,
    }));
  });
});

describe('partner codes', () => {
  it('requires a name, a code and a group before a partner code can be confirmed', async () => {
    stubBase();
    stubPartners();
    vi.spyOn(adminApi, 'createInvites').mockResolvedValue({ codes: [] });
    await mount(AdminInvites);

    button(host, t('createPartnerCode')).click();
    await settle();
    expect(panels.textContent).toContain(t('invitePartnerName'));
    expect(panels.textContent).not.toContain(t('inviteCount'));

    // No name, no code, no group yet: nothing to confirm with.
    expect(() => button(panels, t('createPartnerCode'))).toThrow();

    input(fieldFor(panels, t('invitePartnerName')).querySelector<HTMLInputElement>('input')!, 'Acme');
    input(fieldFor(panels, t('inviteCustomCode')).querySelector<HTMLInputElement>('input')!, 'ACMEPARTNER');
    await settle();
    expect(() => button(panels, t('createPartnerCode'))).toThrow();

    await choose(fieldFor(panels, t('inviteGroup')).querySelector<HTMLButtonElement>('.oa-select')!, 'Premium');
    button(panels, t('createPartnerCode')).click();
    await settle();
  });

  it('sends the partner fields, defaults to unlimited uses and existing accounts allowed', async () => {
    stubBase();
    stubPartners();
    const create = vi.spyOn(adminApi, 'createInvites').mockResolvedValue({
      codes: [{
        id: 'p1', code: 'ACMEPARTNER', owner_id: '', owner_username: '', owner_nickname: '',
        kind: 'partner', name: 'Acme', allow_existing: true,
        group_id: 'g1', group_name: 'Premium', group_days: 3, group_days_max: 7,
        max_uses: 0, uses: 0, expires_at: 0, revoked_at: 0, note: '', created_by: 'op',
        created_at: Date.now(), status: 'active', claims: 0,
      } satisfies InviteCode],
    });
    await mount(AdminInvites);

    button(host, t('createPartnerCode')).click();
    await settle();
    expect(fieldFor(panels, t('inviteMaxUses')).querySelector<HTMLInputElement>('input')!.value).toBe('0');
    expect(switchFor(panels, t('inviteAllowExisting')).checked).toBe(true);

    input(fieldFor(panels, t('invitePartnerName')).querySelector<HTMLInputElement>('input')!, 'Acme');
    input(fieldFor(panels, t('inviteCustomCode')).querySelector<HTMLInputElement>('input')!, 'ACMEPARTNER');
    await choose(fieldFor(panels, t('inviteGroup')).querySelector<HTMLButtonElement>('.oa-select')!, 'Premium');
    button(panels, t('createPartnerCode')).click();
    await settle();

    expect(create).toHaveBeenCalledWith(expect.objectContaining({
      count: 1, code: 'ACMEPARTNER', kind: 'partner', name: 'Acme', allow_existing: true, max_uses: 0,
    }));
    expect(panels.textContent).toContain('Acme');
  });

  it('lists partner codes in their own card, with registrations split from claims', async () => {
    stubBase();
    vi.spyOn(adminApi, 'invites').mockImplementation(async (query: string) => (query.includes('kind=partner')
      ? {
        codes: [{
          id: 'p1', code: 'ACMEPARTNER', owner_id: '', owner_username: '', owner_nickname: '',
          kind: 'partner', name: 'Acme', allow_existing: true,
          group_id: 'g1', group_name: 'Premium', group_days: 3, group_days_max: 7,
          max_uses: 0, uses: 9, expires_at: 0, revoked_at: 0, note: '', created_by: 'op',
          created_at: Date.now(), status: 'active', claims: 4,
        } satisfies InviteCode],
        total: 1,
      }
      : { codes: [], total: 0 }));
    await mount(AdminInvites);

    expect(host.textContent).toContain('Acme');
    // 9 uses minus 4 claims: 5 registrations, read off no field of its own.
    const row = host.querySelector('#secInvitePartners tbody tr')!;
    expect(row.textContent).toContain('5');
    expect(row.textContent).toContain('4');
  });

  // A partner's code is a word somebody chose. Eight letters long, it used to
  // be split like a generated one, and PARTNERX read as PART-NERX.
  it('shows an eight-letter partner code as it was typed, not split in two', async () => {
    stubBase();
    const partner = {
      id: 'p2', code: 'PARTNERX', owner_id: '', owner_username: '', owner_nickname: '',
      kind: 'partner', name: 'Acme', allow_existing: true,
      group_id: 'g1', group_name: 'Premium', group_days: 3, group_days_max: 7,
      max_uses: 0, uses: 0, expires_at: 0, revoked_at: 0, note: '', created_by: 'op',
      created_at: Date.now(), status: 'active', claims: 0,
    } satisfies InviteCode;
    vi.spyOn(adminApi, 'invites').mockResolvedValue({ codes: [partner], total: 1 });
    await mount(AdminInvites);

    expect(host.textContent).toContain('PARTNERX');
    expect(host.textContent).not.toContain('PART-NERX');
  });

  it('shows the stats card its own partner leaderboard', async () => {
    stubBase();
    stubPartners();
    vi.spyOn(adminApi, 'inviteStats').mockResolvedValue({
      active: 1, uses_total: 9, uses_7d: 2, top_inviters: [],
      partners: [{ id: 'p1', code: 'ACMEPARTNER', name: 'Acme', registrations: 5, claims: 4 }],
    });
    await mount(AdminInvites);

    expect(host.textContent).toContain(t('topPartnerCodes'));
    expect(host.textContent).toContain(t('invitePartnerSummary', { registrations: 5, claims: 4 }));
  });
});

describe('an existing code', () => {
  const code: InviteCode = {
    id: 'inv1', code: 'ABCD1234', owner_id: '', owner_username: '', owner_nickname: '',
    kind: 'batch', name: '', allow_existing: false,
    group_id: 'g1', group_name: 'Premium', group_days: 3, group_days_max: 7,
    max_uses: 10, uses: 2, expires_at: 0, revoked_at: 0, note: 'Partner X',
    created_by: 'op', created_at: Date.now(), status: 'active', claims: 0,
  };
  const uses: InviteUse[] = [
    { user_id: 'u1', username: 'alice', nickname: 'Alice', group_days: 3, created_at: Date.now(), rewarded_at: Date.now(), reward_cards: 2, reward_skipped: '', via: 'register' },
    { user_id: 'u2', username: 'bob', nickname: '', group_days: 7, created_at: Date.now(), rewarded_at: 0, reward_cards: 0, reward_skipped: 'same_ip', via: 'register' },
    { user_id: 'u3', username: 'carol', nickname: '', group_days: 3, created_at: Date.now(), rewarded_at: 0, reward_cards: 0, reward_skipped: '', via: 'claim' },
  ];

  async function openRow(): Promise<void> {
    stubBase();
    vi.spyOn(adminApi, 'invites').mockImplementation(async (query: string) => (query.includes('kind=partner')
      ? { codes: [], total: 0 }
      : { codes: [code], total: 1 }));
    vi.spyOn(adminApi, 'inviteUses').mockResolvedValue({ uses });
    await mount(AdminInvites);
    // The partner card's own table is empty in this stub, so the main list
    // is the only one with a row to click.
    host.querySelector<HTMLTableRowElement>('tbody tr')!.click();
    await settle();
  }

  it('shows the range it grants, and who has used it so far, with claims told apart from registrations', async () => {
    await openRow();
    expect(panels.textContent).toContain('ABCD-1234');
    expect(panels.textContent).toContain(t('invitesDaysRange', { from: 3, to: 7 }));
    expect(panels.textContent).toContain(t('inviteRewardedBadge'));
    expect(panels.textContent).toContain(t('inviteSkippedSameIP'));
    expect(panels.textContent).toContain(t('inviteViaClaim'));
  });

  it('revokes on the second press, and then offers nothing more to revoke', async () => {
    await openRow();
    const revoke = vi.spyOn(adminApi, 'revokeInvite').mockResolvedValue({ code: { ...code, status: 'revoked', revoked_at: Date.now() } });

    button(panels, t('revokeLabel')).click();
    await settle();
    expect(revoke).not.toHaveBeenCalled();

    button(panels, t('revokeLabel')).click();
    await settle();
    expect(revoke).toHaveBeenCalledWith('inv1');
    expect(() => button(panels, t('revokeLabel'))).toThrow();
  });
});
