import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import { createApp, h, nextTick, shallowRef, type App } from 'vue';
import { createMemoryHistory, createRouter } from 'vue-router';
import AdminLeaderboard from '../src/views/admin/AdminLeaderboard.vue';
import { adminApi } from '../src/admin/api';
import * as leaderboardApi from '../src/api/leaderboard';
import { providePanelHost } from '../src/composables/usePanelHost';
import { changeLanguage, t } from '../src/composables/useI18n';
import { provideAdminView } from '../src/views/admin/adminView';

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
