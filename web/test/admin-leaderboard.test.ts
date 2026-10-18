import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import { createApp, h, shallowRef, type App } from 'vue';
import { createMemoryHistory, createRouter } from 'vue-router';
import AdminLeaderboard from '../src/views/admin/AdminLeaderboard.vue';
import { adminApi } from '../src/admin/api';
import * as leaderboardApi from '../src/api/leaderboard';
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
});

afterEach(() => {
  app?.unmount();
  app = undefined;
  document.body.textContent = '';
  vi.restoreAllMocks();
});


async function mount(): Promise<void> {
  // The workbench reads the route's hash, so the page needs a router to read it from.
  const router = createRouter({
    history: createMemoryHistory(),
    routes: [{ path: '/:pathMatch(.*)*', component: { render: () => null } }],
  });
  await router.push('/admin/leaderboard');
  app = createApp({ setup() {
    providePanelHost(shallowRef(panels));
    provideAdminView({ actionsHost: actions, setTitle: () => {}, reload: () => {}, params: [] });
    return () => h(AdminLeaderboard);
  } });
  app.use(router);
  app.mount(host);
  await settle();
}

describe('AdminLeaderboard, saving', () => {
  // The setting is written before the preview is re-read. A preview that
  // cannot be re-read used to be reported as a failed save, and the saved
  // state was already accepted, so the next press did nothing at all.
  it('reports a saved setting as saved when the preview refresh fails', async () => {
    vi.spyOn(adminApi, 'settings').mockResolvedValue({
      settings: { 'leaderboard.show_users': 'false' }, groups: [],
    });
    vi.spyOn(leaderboardApi, 'fetchLeaderboard').mockRejectedValue(new Error('Too many requests'));
    const save = vi.spyOn(adminApi, 'saveSettings').mockResolvedValue({ settings: {} });
    await mount();

    const showUsers = [...host.querySelectorAll<HTMLLabelElement>('.oa-checkbox-field')]
      .find((field) => field.querySelector('span')?.textContent === t('boardShowUsers'))!
      .querySelector<HTMLInputElement>('input[type="checkbox"]')!;
    showUsers.click();
    await settle();
    actions.querySelector<HTMLButtonElement>('.oa-btn.primary')!.click();
    await settle();

    expect(save).toHaveBeenCalledWith({ 'leaderboard.show_users': 'true' });
    expect(actions.textContent).toContain(t('saved'));
    expect(document.body.textContent).not.toContain('Too many requests');
  });
});

describe('AdminLeaderboard, the numbers it saves', () => {
  const nobodyYet = { me: { participants: 0 }, accounts: [] } as never;

  function sizeInput(): HTMLInputElement {
    const field = [...host.querySelectorAll<HTMLElement>('.oa-field')]
      .find((node) => node.querySelector('.oa-field-label')?.textContent?.trim() === t('boardSize'));
    const input = field?.querySelector<HTMLInputElement>('input');
    if (!input) throw new Error('Missing the board size field');
    return input;
  }

  function type(value: string): void {
    sizeInput().value = value;
    sizeInput().dispatchEvent(new Event('input', { bubbles: true }));
  }

  function saveButton(): HTMLButtonElement {
    const found = actions.querySelector<HTMLButtonElement>('button.primary');
    if (!found) throw new Error('Missing the save button');
    return found;
  }

  // A cleared box used to be saved as 20 while the box stayed empty on screen,
  // so the page showed nothing and stored a number. The save now waits for one.
  it('keeps the save shut while the size is blank, and writes no default in its place', async () => {
    vi.spyOn(adminApi, 'settings').mockResolvedValue({ settings: { 'leaderboard.size': '20' }, groups: [] });
    vi.spyOn(leaderboardApi, 'fetchLeaderboard').mockResolvedValue(nobodyYet);
    const save = vi.spyOn(adminApi, 'saveSettings').mockResolvedValue({ settings: {} });
    await mount();

    type('');
    await settle();

    expect(saveButton().disabled).toBe(true);
    expect(host.textContent).toContain(t('settingNumberRequired'));
    saveButton().click();
    await settle();
    expect(save).not.toHaveBeenCalled();
  });

  it('saves the number typed in its place', async () => {
    vi.spyOn(adminApi, 'settings').mockResolvedValue({ settings: { 'leaderboard.size': '20' }, groups: [] });
    vi.spyOn(leaderboardApi, 'fetchLeaderboard').mockResolvedValue(nobodyYet);
    const save = vi.spyOn(adminApi, 'saveSettings').mockResolvedValue({ settings: {} });
    await mount();

    type('');
    await settle();
    type('45');
    await settle();

    expect(saveButton().disabled).toBe(false);
    saveButton().click();
    await settle();
    expect(save).toHaveBeenCalledWith({ 'leaderboard.size': '45' });
  });
});

describe('AdminLeaderboard, a preview that cannot be read', () => {
  // An unreadable preview looked exactly like an empty board, so an operator
  // could take a broken endpoint for a board nobody has played on yet.
  it('says the preview could not load, rather than that nothing has been ranked', async () => {
    vi.spyOn(adminApi, 'settings').mockResolvedValue({ settings: {}, groups: [] });
    vi.spyOn(leaderboardApi, 'fetchLeaderboard').mockRejectedValue(new Error('Too many requests'));
    await mount();

    const preview = host.querySelector<HTMLElement>('#secBoardPreview')!;
    expect(preview.textContent).toContain(t('couldNotLoad'));
    expect(preview.textContent).not.toContain(t('nothingYet'));
  });
});
