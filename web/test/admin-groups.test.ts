import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import { createApp, h, shallowRef, type App } from 'vue';
import AdminGroups from '../src/views/admin/AdminGroups.vue';
import { adminApi, type Group, type QuotaPolicy } from '../src/admin/api';
import { ApiError } from '../src/api/client';
import { providePanelHost } from '../src/composables/usePanelHost';
import { changeLanguage, t } from '../src/composables/useI18n';
import { provideAdminView } from '../src/views/admin/adminView';
import { settle } from './helpers';

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
  vi.spyOn(adminApi, 'groups').mockResolvedValue({ groups: [], policies: [] });
  vi.spyOn(adminApi, 'modelOptions').mockResolvedValue({ models: [] });
});

afterEach(() => {
  app?.unmount();
  app = undefined;
  document.body.textContent = '';
  vi.restoreAllMocks();
});


async function mount(): Promise<void> {
  app = createApp({ setup() {
    providePanelHost(shallowRef(panels));
    provideAdminView({ actionsHost: actions, setTitle: () => {}, reload: () => {}, params: [] });
    return () => h(AdminGroups);
  } });
  app.mount(host);
  await settle();
}

function fieldFor(root: ParentNode, label: string): HTMLElement {
  const found = [...root.querySelectorAll<HTMLElement>('.oa-field')].find((field) =>
    field.querySelector('.oa-field-label')?.textContent === label);
  if (!found) throw new Error(`Field not found: ${label}`);
  return found;
}

function input(node: HTMLInputElement, value: string): void {
  node.value = value;
  node.dispatchEvent(new Event('input', { bubbles: true }));
}

function confirmButton(): HTMLButtonElement {
  return panels.querySelector<HTMLButtonElement>('.oa-panel-foot .oa-btn.primary')!;
}

/** Opens the add-group panel and gives the group a name. */
async function startNewGroup(name: string): Promise<void> {
  actions.querySelector<HTMLButtonElement>('#addGroup')!.click();
  await settle();
  input(fieldFor(panels, t('name')).querySelector<HTMLInputElement>('input')!, name);
  await settle();
}

const created = { id: 'g9', name: 'Pro', description: '', is_default: false, allow_all_models: false, members: 0 } as Group;

describe('AdminGroups, creating a group with a quota policy', () => {
  // The group is committed before its policy is saved. A failed policy must
  // leave the panel able to finish the job, not a second create that the
  // unique name index refuses.
  it('turns a created group into an update when its policy save fails', async () => {
    const create = vi.spyOn(adminApi, 'createGroup').mockResolvedValue({ group: created });
    const update = vi.spyOn(adminApi, 'updateGroup').mockResolvedValue({ group: created });
    const policy = vi.spyOn(adminApi, 'savePolicy')
      .mockRejectedValueOnce(new ApiError(500, 'internal', 'The policy store is busy.'))
      .mockResolvedValue({ policy: {} as never });
    await mount();
    await startNewGroup('Pro');

    confirmButton().click();
    await settle();
    expect(create).toHaveBeenCalledTimes(1);
    expect(panels.textContent).toContain('The policy store is busy.');

    confirmButton().click();
    await settle();
    expect(create).toHaveBeenCalledTimes(1);
    expect(update).toHaveBeenCalledWith('g9', expect.objectContaining({ name: 'Pro' }));
    expect(policy).toHaveBeenLastCalledWith(expect.objectContaining({ scope_id: 'g9' }));
  });

  // The server reads quota figures as int64s, and a fraction is refused only
  // after the group exists. The figure is checked before anything is written.
  it('refuses a fractional quota before creating anything', async () => {
    const create = vi.spyOn(adminApi, 'createGroup').mockResolvedValue({ group: created });
    const policy = vi.spyOn(adminApi, 'savePolicy').mockResolvedValue({ policy: {} as never });
    await mount();
    await startNewGroup('Pro');
    input(fieldFor(panels, t('requestsPerMinute')).querySelector<HTMLInputElement>('input')!, '2.5');
    await settle();

    confirmButton().click();
    await settle();

    expect(create).not.toHaveBeenCalled();
    expect(policy).not.toHaveBeenCalled();
    expect(panels.textContent).toContain(t('groupQuotaNotWhole'));
  });

  // A negative figure is refused by the server only after the group exists, the
  // same way a fraction is, so it is refused here before anything is written.
  it('refuses a negative request limit before creating anything', async () => {
    const create = vi.spyOn(adminApi, 'createGroup').mockResolvedValue({ group: created });
    const policy = vi.spyOn(adminApi, 'savePolicy').mockResolvedValue({ policy: {} as never });
    await mount();
    await startNewGroup('Pro');
    input(fieldFor(panels, t('requestsPerMinute')).querySelector<HTMLInputElement>('input')!, '-2');
    await settle();

    confirmButton().click();
    await settle();

    expect(create).not.toHaveBeenCalled();
    expect(policy).not.toHaveBeenCalled();
    expect(panels.textContent).toContain(t('groupQuotaNegative'));
  });

  it('refuses a negative credits limit before creating anything', async () => {
    const create = vi.spyOn(adminApi, 'createGroup').mockResolvedValue({ group: created });
    await mount();
    await startNewGroup('Pro');
    input(fieldFor(panels, t('limitCredits')).querySelector<HTMLInputElement>('input')!, '-1.5');
    await settle();

    confirmButton().click();
    await settle();

    expect(create).not.toHaveBeenCalled();
    expect(panels.textContent).toContain(t('groupQuotaNegative'));
  });

  // Credits are a float64 on the server and the field steps by tenths, so a
  // fraction there is an ordinary value. Refusing it would block the save.
  it('sends a fractional credits limit on a window without refusing it', async () => {
    const create = vi.spyOn(adminApi, 'createGroup').mockResolvedValue({ group: created });
    const policy = vi.spyOn(adminApi, 'savePolicy').mockResolvedValue({ policy: {} as never });
    await mount();
    await startNewGroup('Pro');
    input(fieldFor(panels, t('limitCredits')).querySelector<HTMLInputElement>('input')!, '2.5');
    await settle();

    confirmButton().click();
    await settle();

    expect(create).toHaveBeenCalledTimes(1);
    expect(policy).toHaveBeenCalledWith(expect.objectContaining({
      windows: expect.objectContaining({ '5h': expect.objectContaining({ credits: 2.5 }) }),
    }));
    expect(panels.textContent).not.toContain(t('groupQuotaNotWhole'));
  });

  // A zero left in a window field is sent as "no limit" rather than failing the
  // whole save, which is what windowFigure does. The zero is not a refusal.
  it('sends a zero window figure as no limit instead of refusing the save', async () => {
    const create = vi.spyOn(adminApi, 'createGroup').mockResolvedValue({ group: created });
    const policy = vi.spyOn(adminApi, 'savePolicy').mockResolvedValue({ policy: {} as never });
    await mount();
    await startNewGroup('Pro');
    input(fieldFor(panels, t('limitRequests')).querySelector<HTMLInputElement>('input')!, '0');
    await settle();

    confirmButton().click();
    await settle();

    expect(create).toHaveBeenCalledTimes(1);
    expect(policy).toHaveBeenCalledWith(expect.objectContaining({
      windows: expect.objectContaining({ '5h': expect.objectContaining({ requests: null }) }),
    }));
    expect(panels.textContent).not.toContain(t('groupQuotaNegative'));
  });
});

describe('AdminGroups, the limits a group shows in its list', () => {
  // A window can carry requests and tokens at once. One badge per window
  // showed only the first figure set, so the list understated the policy the
  // form would show.
  it('shows each figure a window sets as its own badge', async () => {
    const group = { id: 'g1', name: 'Pro', description: '', is_default: false, allow_all_models: true, members: 0 } as unknown as Group;
    const policy = {
      id: 'p1', scope: 'group', scope_id: 'g1', rpm: null, tpm: null, updated_at: 0,
      windows: {
        '5h': { enabled: true, requests: 50, tokens: 1000, credits: null },
        '1w': { enabled: false, requests: 9, tokens: null, credits: null },
        '1m': { enabled: null, requests: null, tokens: null, credits: null },
      },
    } as unknown as QuotaPolicy;
    vi.spyOn(adminApi, 'groups').mockResolvedValue({ groups: [group], policies: [policy] });
    await mount();

    const badges = [...host.querySelectorAll('.oa-badge')].map((node) => node.textContent?.trim());
    expect(badges).toContain(`5h: ${t('boardRequests', { count: 50 })}`);
    expect(badges).toContain(`5h: ${t('boardTokens', { count: 1000 })}`);
    // A window the operator left unenforced shows nothing.
    expect(badges.some((text) => text?.startsWith('1w'))).toBe(false);
  });

  // A credits-only window has no request or token figure to show, so its badge
  // has to carry the credits or the list would show a bare window name.
  it('shows a credits-only window as its credits figure', async () => {
    const group = { id: 'g2', name: 'Credits', description: '', is_default: false, allow_all_models: true, members: 0 } as unknown as Group;
    const policy = {
      id: 'p2', scope: 'group', scope_id: 'g2', rpm: null, tpm: null, updated_at: 0,
      windows: {
        '5h': { enabled: true, requests: null, tokens: null, credits: 2.5 },
      },
    } as unknown as QuotaPolicy;
    vi.spyOn(adminApi, 'groups').mockResolvedValue({ groups: [group], policies: [policy] });
    await mount();

    const badges = [...host.querySelectorAll('.oa-badge')].map((node) => node.textContent?.trim());
    expect(badges).toContain('5h: 2.5');
  });
});
