// The backoffice's bonus page: the bars and what is granted into them, and the
// check-in settings that sit under them. The rules the page edits live on the
// server; what is held here is that every rule the administrator sets reaches
// the request, and that nothing is sent that they did not ask for — an amount
// typed once is granted once.

import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import { createApp, h, nextTick, shallowRef, type App } from 'vue';
import AdminBonus from '../src/views/admin/AdminBonus.vue';
import {
  adminApi, type AdminModel, type BonusBarRow, type BonusGrantRow, type CheckinSettings, type Group,
} from '../src/admin/api';
import { ApiError } from '../src/api/client';
import { provideAdminView } from '../src/views/admin/adminView';
import { providePanelHost } from '../src/composables/usePanelHost';
import { changeLanguage, t } from '../src/composables/useI18n';

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

async function mount(): Promise<void> {
  app = createApp({ setup() {
    providePanelHost(shallowRef(panels));
    provideAdminView({ actionsHost: actions, setTitle: () => {}, reload: () => {}, params: [] });
    return () => h(AdminBonus);
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

function inputOf(root: ParentNode, label: string): HTMLInputElement {
  return fieldFor(root, label).querySelector<HTMLInputElement>('input')!;
}

function switchFor(root: ParentNode, label: string): HTMLInputElement {
  const found = [...root.querySelectorAll<HTMLLabelElement>('.oa-checkbox-field')].find((field) =>
    field.querySelector('span')?.textContent === label);
  if (!found) throw new Error(`Switch not found: ${label}`);
  return found.querySelector<HTMLInputElement>('input[type="checkbox"]')!;
}

function hasSwitch(root: ParentNode, label: string): boolean {
  return [...root.querySelectorAll<HTMLLabelElement>('.oa-checkbox-field')].some((field) =>
    field.querySelector('span')?.textContent === label);
}

async function choose(field: HTMLElement, optionLabel: string): Promise<void> {
  field.querySelector<HTMLButtonElement>('.oa-select')!.click();
  await settle();
  const option = [...document.querySelectorAll<HTMLElement>('[role="option"]')]
    .find((node) => node.textContent?.trim() === optionLabel);
  if (!option) throw new Error(`Option not found: ${optionLabel}`);
  option.click();
  await settle();
}

function type(node: HTMLInputElement, value: string): void {
  node.value = value;
  node.dispatchEvent(new Event('input', { bubbles: true }));
}

function checkRow(root: ParentNode, title: string): HTMLInputElement {
  const found = [...root.querySelectorAll<HTMLLabelElement>('.oa-check-row')].find((row) =>
    row.querySelector('.oa-check-title')?.textContent === title);
  if (!found) throw new Error(`Check row not found: ${title}`);
  return found.querySelector<HTMLInputElement>('input')!;
}

function bar(over: Partial<BonusBarRow> = {}): BonusBarRow {
  return {
    id: 'bar-1', name: 'Launch gift', description: 'For the first week', kind: 'bonus', toggle_mode: 'user',
    default_on: true, show_total: true, model_ids: [], default_expires_at: 0, active: true, created_at: 1,
    granted: 1000, used: 250, holders: 12,
    ...over,
  };
}

function grant(over: Partial<BonusGrantRow> = {}): BonusGrantRow {
  return {
    id: 'g-1', bar_id: 'bar-1', user_id: 'u-1', username: 'alice', amount: 10, used: 4, expires_at: 0,
    source: 'admin', note: '', created_at: Date.now() - 60_000,
    ...over,
  };
}

const model = (id: string, name: string): AdminModel => ({
  id, display_name: name, model_id: `${id}-api`, provider_name: 'Provider',
}) as AdminModel;

const emptyCheckin: CheckinSettings = { enabled: false, timezone: 'Asia/Shanghai', daily: { kind: '' }, rules: [] };

function stub(bars: BonusBarRow[] = [bar()], checkin: CheckinSettings = emptyCheckin) {
  vi.spyOn(adminApi, 'bonusBars').mockResolvedValue({ bars });
  vi.spyOn(adminApi, 'models').mockResolvedValue({ models: [model('m1', 'Alpha'), model('m2', 'Beta')] });
  vi.spyOn(adminApi, 'groups').mockResolvedValue({
    groups: [{ id: 'g1', name: 'Premium' } as Group, { id: 'g2', name: 'Basic' } as Group], policies: [],
  });
  vi.spyOn(adminApi, 'checkinSettings').mockResolvedValue(structuredClone(checkin));
  vi.spyOn(adminApi, 'bonusGrants').mockResolvedValue({ grants: [], total: 0 });
}

function rows(): HTMLElement[] {
  return [...host.querySelectorAll<HTMLElement>('.oa-table tbody tr')];
}

describe('the list of bars', () => {
  it('says how each bar is spent, and marks the ones that are switched off or keep their amounts private', async () => {
    stub([
      bar({ id: 'a', name: 'Free choice' }),
      bar({ id: 'b', name: 'Forced', toggle_mode: 'on', show_total: false }),
      bar({ id: 'c', name: 'Spare', kind: 'reserve', toggle_mode: 'off' }),
      bar({ id: 'd', name: 'Old', active: false }),
    ]);
    await mount();

    const list = rows();
    expect(list).toHaveLength(4);
    expect(list[0]!.textContent).toContain(t('bonusModeUser'));
    expect(list[1]!.textContent).toContain(t('bonusModeOn'));
    expect(list[1]!.textContent).toContain(t('bonusTotalHidden'));
    // A reserve has no switch to describe: it is what it is.
    expect(list[2]!.textContent).toContain(t('bonusKindReserve'));
    expect(list[2]!.textContent).not.toContain(t('bonusModeOff'));
    expect(list[3]!.textContent).toContain(t('bonusInactive'));
    expect(list[3]!.classList.contains('muted')).toBe(true);
    // Totals for the bar: granted, used, holders.
    expect(list[0]!.textContent).toContain('1000');
    expect(list[0]!.textContent).toContain('250');
    expect(list[0]!.textContent).toContain('12');
  });

  it('says so when there are none, and still offers the check-in settings', async () => {
    stub([]);
    await mount();
    expect(host.textContent).toContain(t('noBonusBars'));
    expect(host.textContent).toContain(t('checkinEnabled'));
  });

  it('shows why nothing loaded, and tries again on request', async () => {
    const list = vi.spyOn(adminApi, 'bonusBars').mockRejectedValueOnce(new Error('database is locked'));
    vi.spyOn(adminApi, 'models').mockResolvedValue({ models: [] });
    vi.spyOn(adminApi, 'groups').mockResolvedValue({ groups: [], policies: [] });
    vi.spyOn(adminApi, 'checkinSettings').mockResolvedValue(emptyCheckin);
    await mount();
    expect(host.textContent).toContain('database is locked');

    list.mockResolvedValue({ bars: [bar()] });
    button(host, t('tryAgain')).click();
    await settle();
    expect(host.textContent).not.toContain('database is locked');
    expect(rows()).toHaveLength(1);
  });

  it('orders the rows by a column once its header is pressed', async () => {
    stub([bar({ id: 'a', name: 'Small', granted: 5 }), bar({ id: 'b', name: 'Big', granted: 500 })]);
    await mount();
    expect(rows()[0]!.textContent).toContain('Small');

    const header = [...host.querySelectorAll<HTMLElement>('th')].find((th) => th.textContent?.trim() === t('colBonusGranted'))!;
    header.click();
    await settle();
    header.click();
    await settle();
    expect(rows()[0]!.textContent).toContain('Big');
  });
});

describe('creating a bar', () => {
  it('sends every rule that was set, and shows the bar it made', async () => {
    stub([]);
    const save = vi.spyOn(adminApi, 'saveBonusBar').mockResolvedValue({ bar: bar() });
    await mount();

    button(actions, t('addBonusBar')).click();
    await settle();
    expect(panels.querySelector('.oa-panel')).not.toBeNull();

    type(inputOf(panels, t('bonusName')), 'Beta reward');
    type(inputOf(panels, t('bonusDescription')), 'Thanks for testing');
    await choose(fieldFor(panels, t('bonusMode')), t('bonusModeOn'));
    // The default matters only where the account chooses.
    expect(hasSwitch(panels, t('bonusDefaultOn'))).toBe(false);
    switchFor(panels, t('bonusShowTotal')).click();
    await settle();
    checkRow(panels, 'Beta').click();
    await settle();
    type(inputOf(panels, t('bonusDefaultExpiry')), '2031-05-06T12:30');
    await settle();

    button(panels, t('save')).click();
    await settle();

    expect(save).toHaveBeenCalledTimes(1);
    expect(save).toHaveBeenCalledWith(null, {
      name: 'Beta reward', description: 'Thanks for testing', kind: 'bonus', toggle_mode: 'on',
      default_on: true, show_total: false, model_ids: ['m2'],
      default_expires_at: new Date('2031-05-06T12:30').getTime(), active: true,
    });
    // The panel closes and the list is read again.
    expect(panels.querySelector('.oa-panel')).toBeNull();
    expect(adminApi.bonusBars).toHaveBeenCalledTimes(2);
  });

  it('takes the switch away from a reserve, which has none', async () => {
    stub([]);
    const save = vi.spyOn(adminApi, 'saveBonusBar').mockResolvedValue({ bar: bar() });
    await mount();

    button(actions, t('addBonusBar')).click();
    await settle();
    expect(fieldFor(panels, t('bonusMode'))).not.toBeNull();

    await choose(fieldFor(panels, t('bonusKind')), t('bonusKindReserve'));
    expect(panels.textContent).not.toContain(t('bonusMode'));

    type(inputOf(panels, t('bonusName')), 'Spare');
    button(panels, t('save')).click();
    await settle();
    expect(save).toHaveBeenCalledWith(null, expect.objectContaining({ kind: 'reserve', name: 'Spare' }));
  });

  it('only offers a starting position for a bar the account may switch', async () => {
    stub([]);
    await mount();
    button(actions, t('addBonusBar')).click();
    await settle();
    expect(hasSwitch(panels, t('bonusDefaultOn'))).toBe(true);

    await choose(fieldFor(panels, t('bonusMode')), t('bonusModeOff'));
    expect(hasSwitch(panels, t('bonusDefaultOn'))).toBe(false);
  });

  it('offers no in-use switch for a bar that does not exist yet', async () => {
    stub([]);
    await mount();
    button(actions, t('addBonusBar')).click();
    await settle();
    expect(hasSwitch(panels, t('bonusActive'))).toBe(false);
    expect(panels.textContent).not.toContain(t('bonusGrantsHeading'));
  });

  it('says what the server refused, in the panel, and leaves it open', async () => {
    stub([]);
    vi.spyOn(adminApi, 'saveBonusBar').mockRejectedValue(new ApiError(400, 'invalid_request', 'A bar needs a name.'));
    await mount();

    button(actions, t('addBonusBar')).click();
    await settle();
    button(panels, t('save')).click();
    await settle();

    expect(panels.querySelector('.oa-drawer-flash.visible')?.textContent).toBe('A bar needs a name.');
    expect(panels.querySelector('.oa-panel')).not.toBeNull();
  });
});

describe('editing a bar', () => {
  it('opens with what the bar holds now, and saves under its own id', async () => {
    const expiry = new Date(2031, 4, 6, 12, 30).getTime();
    stub([bar({ model_ids: ['m1'], default_expires_at: expiry, show_total: false, toggle_mode: 'off' })]);
    const save = vi.spyOn(adminApi, 'saveBonusBar').mockResolvedValue({ bar: bar() });
    await mount();

    rows()[0]!.click();
    await settle();

    expect(inputOf(panels, t('bonusName')).value).toBe('Launch gift');
    expect(inputOf(panels, t('bonusDefaultExpiry')).value).toBe('2031-05-06T12:30');
    expect(checkRow(panels, 'Alpha').checked).toBe(true);
    expect(checkRow(panels, 'Beta').checked).toBe(false);
    expect(switchFor(panels, t('bonusShowTotal')).checked).toBe(false);
    expect(fieldFor(panels, t('bonusMode')).textContent).toContain(t('bonusModeOff'));
    // Existing bars can be switched off; the grants are read for this one.
    expect(switchFor(panels, t('bonusActive')).checked).toBe(true);
    expect(adminApi.bonusGrants).toHaveBeenCalledWith('bar-1', 0);

    switchFor(panels, t('bonusActive')).click();
    await settle();
    button(panels, t('save')).click();
    await settle();

    expect(save).toHaveBeenCalledWith('bar-1', expect.objectContaining({
      name: 'Launch gift', toggle_mode: 'off', show_total: false, model_ids: ['m1'], default_expires_at: expiry, active: false,
    }));
  });

  it('clears the expiry when the field is emptied, rather than sending zero as a date', async () => {
    stub([bar({ default_expires_at: Date.now() + 86_400_000 })]);
    const save = vi.spyOn(adminApi, 'saveBonusBar').mockResolvedValue({ bar: bar() });
    await mount();

    rows()[0]!.click();
    await settle();
    type(inputOf(panels, t('bonusDefaultExpiry')), '');
    await settle();
    button(panels, t('save')).click();
    await settle();

    expect(save).toHaveBeenCalledWith('bar-1', expect.objectContaining({ default_expires_at: 0 }));
  });

  it('asks before it deletes, and deletes only the bar that was open', async () => {
    stub([bar({ id: 'x', name: 'Doomed' }), bar({ id: 'y', name: 'Kept' })]);
    const remove = vi.spyOn(adminApi, 'deleteBonusBar').mockResolvedValue(undefined);
    await mount();

    rows()[0]!.click();
    await settle();
    button(panels, t('deleteLabel')).click();
    await settle();

    expect(panels.textContent).toContain(t('confirmDeleteBonus', { name: 'Doomed' }));
    expect(remove).not.toHaveBeenCalled();

    button(panels, t('deleteLabel')).click();
    await settle();
    expect(remove).toHaveBeenCalledTimes(1);
    expect(remove).toHaveBeenCalledWith('x');
    expect(panels.querySelector('.oa-panel')).toBeNull();
  });
});

describe('granting', () => {
  async function openGrant(): Promise<void> {
    rows()[0]!.click();
    await settle();
    button(panels, t('bonusGrantAction')).click();
    await settle();
  }

  it('grants to everyone with just an amount, and does not invent an expiry', async () => {
    stub();
    const send = vi.spyOn(adminApi, 'grantBonus').mockResolvedValue({ granted: 40, amount: 5, expires_at: 0 });
    await mount();

    await openGrant();
    expect(panels.querySelector('.oa-panel-title')?.textContent).toBe(t('bonusGrantTitle', { name: 'Launch gift' }));
    // A grant to everyone is not what the panel starts on.
    expect(fieldFor(panels, t('bonusGrantTo')).querySelector('.oa-select-label')?.textContent).toBe(t('bonusGrantNamed'));
    await choose(fieldFor(panels, t('bonusGrantTo')), t('bonusGrantEveryone'));
    type(inputOf(panels, t('bonusGrantAmount')), '5');
    await settle();
    button(panels, t('bonusGrantAction')).click();
    await settle();

    expect(send).toHaveBeenCalledWith('bar-1', { amount: 5, all: true, note: '' });
    // Back on the bar, with what happened said.
    expect(panels.textContent).toContain(t('bonusGrantedOther', { count: 40 }));
  });

  it('grants to a group by its id', async () => {
    stub();
    const send = vi.spyOn(adminApi, 'grantBonus').mockResolvedValue({ granted: 1, amount: 2, expires_at: 0 });
    await mount();

    await openGrant();
    await choose(fieldFor(panels, t('bonusGrantTo')), t('bonusGrantGroup'));
    await choose(fieldFor(panels, t('bonusGrantGroupField')), 'Basic');
    type(inputOf(panels, t('bonusGrantAmount')), '2.5');
    await settle();
    button(panels, t('bonusGrantAction')).click();
    await settle();

    expect(send).toHaveBeenCalledWith('bar-1', { amount: 2.5, group_id: 'g2', note: '' });
    expect(panels.textContent).toContain(t('bonusGrantedOne'));
  });

  it('grants to named accounts, split on commas, with a lifetime and a note', async () => {
    stub();
    const send = vi.spyOn(adminApi, 'grantBonus').mockResolvedValue({ granted: 2, amount: 3, expires_at: 1 });
    await mount();

    await openGrant();
    type(inputOf(panels, t('bonusGrantNamesField')), ' alice, bob ,, ');
    type(inputOf(panels, t('bonusGrantAmount')), '3');
    type(inputOf(panels, t('bonusGrantDays')), '7');
    type(inputOf(panels, t('bonusGrantNote')), 'with thanks');
    await settle();
    button(panels, t('bonusGrantAction')).click();
    await settle();

    expect(send).toHaveBeenCalledWith('bar-1', {
      amount: 3, usernames: ['alice', 'bob'], days: 7, note: 'with thanks',
    });
  });

  it('stays on the grant panel and says why when the server refuses', async () => {
    stub();
    vi.spyOn(adminApi, 'grantBonus').mockRejectedValue(new ApiError(404, 'not_found', 'No account nobody.'));
    await mount();

    await openGrant();
    type(inputOf(panels, t('bonusGrantNamesField')), 'nobody');
    type(inputOf(panels, t('bonusGrantAmount')), '3');
    await settle();
    button(panels, t('bonusGrantAction')).click();
    await settle();

    expect(panels.querySelector('.oa-drawer-flash.visible')?.textContent).toBe('No account nobody.');
    expect(panels.querySelector('.oa-panel-title')?.textContent).toBe(t('bonusGrantTitle', { name: 'Launch gift' }));
  });

  it('goes back to the bar with the back arrow', async () => {
    stub();
    await mount();
    await openGrant();

    panels.querySelector<HTMLButtonElement>(`button[aria-label="${t('back')}"]`)!.click();
    await settle();
    expect(panels.querySelector('.oa-panel-title')?.textContent).toBe('Launch gift');
  });
});

describe('what is granted', () => {
  it('lists each grant with what is left of it, when it lapses and where it came from', async () => {
    stub();
    vi.spyOn(adminApi, 'bonusGrants').mockResolvedValue({
      grants: [
        grant({ id: 'g-1', username: 'alice', amount: 10, used: 4, note: 'apology' }),
        grant({ id: 'g-2', username: 'bob', amount: 2, used: 2, source: 'checkin', expires_at: Date.UTC(2031, 0, 1) }),
      ],
      total: 2,
    });
    await mount();

    rows()[0]!.click();
    await settle();
    const list = [...panels.querySelectorAll<HTMLElement>('.oa-card-row')];
    expect(list).toHaveLength(2);
    expect(list[0]!.textContent).toContain('alice');
    expect(list[0]!.textContent).toContain('4 / 10');
    expect(list[0]!.textContent).toContain(t('bonusNoExpiry'));
    expect(list[0]!.textContent).toContain('apology');
    expect(list[1]!.textContent).toContain(t('bonusSourceCheckin'));
    // Nothing left of the second grant to take back.
    expect(list[0]!.querySelector('button')!.disabled).toBe(false);
    expect(list[1]!.querySelector('button')!.disabled).toBe(true);
  });

  it('says when nothing has been granted yet', async () => {
    stub();
    await mount();
    rows()[0]!.click();
    await settle();
    expect(panels.textContent).toContain(t('bonusNoGrants'));
  });

  it('takes a grant back and reads the list and the totals again', async () => {
    stub();
    const list = vi.spyOn(adminApi, 'bonusGrants').mockResolvedValue({ grants: [grant()], total: 1 });
    const revoke = vi.spyOn(adminApi, 'revokeBonusGrant').mockResolvedValue({ revoked: 6 });
    await mount();

    rows()[0]!.click();
    await settle();
    list.mockClear();
    button(panels, t('bonusRevoke')).click();
    await settle();

    expect(revoke).toHaveBeenCalledWith('g-1');
    expect(panels.textContent).toContain(t('bonusRevoked', { amount: '6' }));
    expect(list).toHaveBeenCalledWith('bar-1', 0);
    expect(adminApi.bonusBars).toHaveBeenCalledTimes(2);
  });

  it('pages: the next twenty are added to the ones shown, not swapped for them', async () => {
    stub();
    const list = vi.spyOn(adminApi, 'bonusGrants').mockImplementation(async (_id, offset = 0) => ({
      grants: [grant({ id: `g-${offset}`, username: offset ? 'second-page' : 'first-page' })],
      total: 2,
    }));
    await mount();

    rows()[0]!.click();
    await settle();
    button(panels, t('bonusLoadMore')).click();
    await settle();

    expect(list).toHaveBeenLastCalledWith('bar-1', 1);
    expect(panels.textContent).toContain('first-page');
    expect(panels.textContent).toContain('second-page');
    // Everything is shown, so the button is gone.
    expect([...panels.querySelectorAll('button')].some((node) => node.textContent?.trim() === t('bonusLoadMore'))).toBe(false);
  });
});

describe('check-in settings', () => {
  it('saves the switch, the time zone and a milestone as one document', async () => {
    stub([bar(), bar({ id: 'bar-2', name: 'Second' })]);
    const save = vi.spyOn(adminApi, 'saveCheckinSettings').mockImplementation(async (body) => body);
    await mount();

    switchFor(host, t('checkinEnabled')).click();
    await settle();
    type(inputOf(host, t('checkinTimezone')), 'UTC');
    button(host, t('checkinAddRule')).click();
    await settle();
    type(inputOf(host, t('checkinRuleTitleField')), 'First week');
    await settle();
    button(host, t('checkinSave')).click();
    await settle();

    expect(save).toHaveBeenCalledTimes(1);
    const sent = save.mock.calls[0]![0];
    expect(sent.enabled).toBe(true);
    expect(sent.timezone).toBe('UTC');
    expect(sent.daily).toEqual({ kind: '' });
    expect(sent.rules).toEqual([{
      id: expect.stringMatching(/^m/), title: 'First week', basis: 'streak', days: 7,
      // The newest bar is the one an administrator most often means: the list
      // is newest-first, so the first is what a new milestone starts with.
      reward: { kind: 'bonus', bar_id: 'bar-1', amount: 1, valid_days: 7 },
    }]);
    expect(host.textContent).toContain(t('checkinSaved'));
  });

  it('gives two new milestones ids that differ, so neither overwrites the other on save', async () => {
    stub();
    const save = vi.spyOn(adminApi, 'saveCheckinSettings').mockImplementation(async (body) => body);
    await mount();

    button(host, t('checkinAddRule')).click();
    await settle();
    button(host, t('checkinAddRule')).click();
    await settle();
    button(host, t('checkinSave')).click();
    await settle();

    const ids = save.mock.calls[0]![0].rules.map((rule) => rule.id);
    expect(ids).toHaveLength(2);
    expect(new Set(ids).size).toBe(2);
  });

  it('starts a milestone from what the settings already hold, and removes the one pressed', async () => {
    stub([bar()], {
      enabled: true, timezone: 'Asia/Shanghai', daily: { kind: 'bonus', bar_id: 'bar-1', amount: 0.5, valid_days: 3 },
      rules: [
        { id: 'w', title: 'Week', basis: 'streak', days: 7, reward: { kind: 'bonus', bar_id: 'bar-1', amount: 2, valid_days: 7 } },
        { id: 'm', title: 'Month', basis: 'month', days: 20, reward: { kind: 'card', name: 'Reset', windows: [], cards: 1, valid_days: 30 } },
      ],
    });
    const save = vi.spyOn(adminApi, 'saveCheckinSettings').mockImplementation(async (body) => body);
    await mount();

    expect(switchFor(host, t('checkinEnabled')).checked).toBe(true);
    expect(inputOf(host, t('checkinTimezone')).value).toBe('Asia/Shanghai');

    const removers = [...host.querySelectorAll<HTMLButtonElement>('button')].filter((node) => node.textContent?.trim() === t('checkinRemoveRule'));
    expect(removers).toHaveLength(2);
    removers[0]!.click();
    await settle();
    button(host, t('checkinSave')).click();
    await settle();

    const sent = save.mock.calls[0]![0];
    expect(sent.rules.map((rule) => rule.id)).toEqual(['m']);
    expect(sent.daily).toEqual({ kind: 'bonus', bar_id: 'bar-1', amount: 0.5, valid_days: 3 });
  });

  it('says what the server refused', async () => {
    stub();
    vi.spyOn(adminApi, 'saveCheckinSettings').mockRejectedValue(new ApiError(400, 'invalid_request', 'Time zone unknown.'));
    await mount();

    button(host, t('checkinSave')).click();
    await settle();
    expect(host.textContent).toContain('Time zone unknown.');
    expect(host.textContent).not.toContain(t('checkinSaved'));
  });
});

describe('the reward editor', () => {
  async function editorSelect(): Promise<HTMLElement> {
    stub([bar()]);
    await mount();
    return fieldFor(host, t('checkinDailyHeading'));
  }

  it('offers nothing as an answer for the daily reward, and starts a bonus from the first bar', async () => {
    const save = vi.spyOn(adminApi, 'saveCheckinSettings').mockImplementation(async (body) => body);
    const daily = await editorSelect();
    expect(daily.querySelector('.oa-select-label')?.textContent).toBe(t('checkinRewardNone'));

    await choose(daily, t('checkinRewardBonusKind'));
    type(inputOf(host, t('checkinRewardAmount')), '0.5');
    await settle();
    button(host, t('checkinSave')).click();
    await settle();

    expect(save.mock.calls[0]![0].daily).toEqual({ kind: 'bonus', bar_id: 'bar-1', amount: 0.5, valid_days: 7 });
  });

  it('starts cards from cards’ own defaults, and drops the bonus fields it had', async () => {
    const save = vi.spyOn(adminApi, 'saveCheckinSettings').mockImplementation(async (body) => body);
    const daily = await editorSelect();

    await choose(daily, t('checkinRewardBonusKind'));
    await choose(fieldFor(host, t('checkinDailyHeading')), t('checkinRewardCardKind'));
    type(inputOf(host, t('checkinRewardCardName')), 'Weekly reset');
    await settle();
    await choose(fieldFor(host, t('checkinRewardCardScope')), t('cardReset1W'));
    button(host, t('checkinSave')).click();
    await settle();

    // No `bar_id` or `amount` carried over from the bonus it was a moment ago.
    expect(save.mock.calls[0]![0].daily).toEqual({
      kind: 'card', name: 'Weekly reset', windows: ['1w'], cards: 1, valid_days: 30,
    });
  });

  it('does not offer switched-off bars as somewhere to pay a reward', async () => {
    stub([bar({ id: 'a', name: 'Live' }), bar({ id: 'b', name: 'Retired', active: false })]);
    await mount();

    await choose(fieldFor(host, t('checkinDailyHeading')), t('checkinRewardBonusKind'));
    const field = fieldFor(host, t('checkinRewardBar'));
    field.querySelector<HTMLButtonElement>('.oa-select')!.click();
    await settle();
    // Menus of the selects used before stay mounted; the bars are the ones naming a bar.
    const menu = [...document.querySelectorAll<HTMLElement>('.oa-select-menu')].find((node) => node.textContent?.includes('Live'))!;
    expect([...menu.querySelectorAll('[role="option"]')].map((node) => node.textContent?.trim())).toEqual(['Live']);
  });
});
