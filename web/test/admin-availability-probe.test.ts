// The model test's summary counts what the server counted. A model whose
// provider is switched off is neither answered nor failed, and the reader is
// told how many were left out, rather than having to subtract them.

import { afterEach, beforeEach, expect, it, vi } from 'vitest';
import { createApp, h, nextTick, type App } from 'vue';
import { createMemoryHistory, createRouter } from 'vue-router';
import { adminApi } from '../src/admin/api';
import { changeLanguage, t } from '../src/composables/useI18n';
import AdminAvailability from '../src/views/admin/AdminAvailability.vue';
import { provideAdminView } from '../src/views/admin/adminView';

let app: App | undefined;
let host: HTMLElement;
let actions: HTMLElement;

async function settle(): Promise<void> {
  await new Promise((resolve) => setTimeout(resolve, 0));
  await nextTick();
}

beforeEach(async () => {
  await changeLanguage('en');
  host = document.createElement('div');
  actions = document.createElement('div');
  document.body.append(host, actions);
  vi.spyOn(adminApi, 'settings').mockResolvedValue({
    settings: {
      'health.window_minutes': '30',
      'health.disable_after': '5',
      'health.disable_below': '0',
      'health.warn_below': '90',
      'health.retain_days': '14',
    },
  });
  vi.spyOn(adminApi, 'health').mockResolvedValue({ hours: 24, models: [], policy: {} } as never);
});

afterEach(() => {
  app?.unmount();
  app = undefined;
  host.remove();
  actions.remove();
  document.body.textContent = '';
  vi.restoreAllMocks();
});

async function mountPage(): Promise<void> {
  const router = createRouter({
    history: createMemoryHistory(),
    routes: [{ path: '/:pathMatch(.*)*', component: { render: () => null } }],
  });
  app = createApp({
    setup() {
      provideAdminView({ actionsHost: actions, setTitle() {}, reload() {}, params: [] });
      return () => h(AdminAvailability);
    },
  });
  app.use(router);
  app.mount(host);
  await settle();
}

it('says how many models were skipped because their provider is switched off', async () => {
  vi.spyOn(adminApi, 'probeAllModels').mockResolvedValue({
    completed: 10, total: 10, succeeded: 8, failed: 1, skipped: 1,
  });
  await mountPage();

  const probe = [...document.querySelectorAll<HTMLButtonElement>('button')]
    .find((button) => button.textContent?.trim() === t('probeAllModels'));
  expect(probe).toBeDefined();
  probe!.click();
  await settle();

  expect(document.querySelector('.oa-control-flash')?.textContent).toBe(
    t('probeAllModelsDone', { succeeded: 8, total: 10, failed: 1, skipped: 1 }),
  );
  expect(document.querySelector('.oa-control-flash')?.textContent).toContain('1 skipped');
});
