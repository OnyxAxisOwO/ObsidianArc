// A reset that left a model off says which one. The response used to be
// thrown away, so the page reported success while a model it promised to
// switch back on stayed off, and the administrator had no way to find it.

import { afterEach, beforeEach, expect, it, vi } from 'vitest';
import { createApp, h, type App } from 'vue';
import { createMemoryHistory, createRouter } from 'vue-router';
import { adminApi, type ModelHealth } from '../src/admin/api';
import { changeLanguage, t } from '../src/composables/useI18n';
import AdminAvailability from '../src/views/admin/AdminAvailability.vue';
import { provideAdminView } from '../src/views/admin/adminView';
import { settle } from './helpers';

let app: App | undefined;
let host: HTMLElement;
let actions: HTMLElement;

function healthOf(id: string, name: string): ModelHealth {
  return {
    model_id: id,
    name,
    provider: 'Upstream',
    enabled: false,
    auto_disabled: true,
    status: { state: 'down', uptime: 0, samples: 0, user_samples: 0, system_samples: 0 },
  } as ModelHealth;
}

beforeEach(async () => {
  await changeLanguage('en');
  host = document.createElement('div');
  actions = document.createElement('div');
  document.body.append(host, actions);
  vi.spyOn(adminApi, 'settings').mockResolvedValue({ settings: {} });
  vi.spyOn(adminApi, 'health').mockResolvedValue({
    hours: 24,
    models: [healthOf('m1', 'Kept Off'), healthOf('m2', 'Back On')],
    policy: {},
  } as never);
});

afterEach(() => {
  app?.unmount();
  app = undefined;
  host.remove();
  actions.remove();
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

async function reset(): Promise<void> {
  const button = [...document.querySelectorAll<HTMLButtonElement>('button')]
    .find((candidate) => candidate.textContent?.trim() === t('resetUptime'));
  expect(button).toBeDefined();
  button!.click();
  await settle();
  document.querySelector<HTMLButtonElement>('.oa-btn.armed')!.click();
  await settle();
}

it('names the models a reset could not switch back on', async () => {
  vi.spyOn(adminApi, 'resetHealth').mockResolvedValue({
    reset_at: 1, probes_cleared: 3, models_reenabled: 1, models_not_reenabled: ['m1'],
  });
  await mountPage();
  await reset();

  expect(document.querySelector('.oa-control-flash.ok')?.textContent).toBe(t('resetUptimeDone'));
  expect(document.querySelector('.oa-control-flash[role="alert"]')?.textContent).toBe(
    t('resetUptimeNotReenabled', { models: 'Kept Off' }),
  );
});

// A model the page has never listed is still named, by the identifier the
// server gave, rather than dropped from the sentence.
it('falls back to the identifier for a model the page does not know', async () => {
  vi.spyOn(adminApi, 'resetHealth').mockResolvedValue({
    reset_at: 1, probes_cleared: 0, models_reenabled: 0, models_not_reenabled: ['m1', 'm9'],
  });
  await mountPage();
  await reset();

  expect(document.querySelector('.oa-control-flash[role="alert"]')?.textContent).toBe(
    t('resetUptimeNotReenabled', { models: 'Kept Off, m9' }),
  );
});

it('says nothing more when every model came back', async () => {
  vi.spyOn(adminApi, 'resetHealth').mockResolvedValue({
    reset_at: 1, probes_cleared: 3, models_reenabled: 2,
  });
  await mountPage();
  await reset();

  expect(document.querySelector('.oa-control-flash.ok')?.textContent).toBe(t('resetUptimeDone'));
  expect(document.querySelector('.oa-control-flash[role="alert"]')).toBeNull();
});
