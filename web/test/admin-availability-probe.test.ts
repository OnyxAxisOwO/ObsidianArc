// The model test's summary counts what the server counted. A model whose
// provider is switched off is neither answered nor failed, and the reader is
// told how many were left out, rather than having to subtract them.

import { afterEach, beforeEach, expect, it, vi } from 'vitest';
import { createApp, h, nextTick, type App } from 'vue';
import { createMemoryHistory, createRouter } from 'vue-router';
import { adminApi } from '../src/admin/api';
import { ApiError } from '../src/api/client';
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

// Each result line starts its own timer to clear itself. An earlier run's
// timer used to fire into a later run's line and take it away early, so the
// second result sat on screen for one second instead of five.
it('keeps a newer result for its own five seconds when an older timer is still running', async () => {
  const probeAll = vi.spyOn(adminApi, 'probeAllModels')
    .mockResolvedValueOnce({ completed: 1, total: 1, succeeded: 1, failed: 0, skipped: 0 })
    .mockResolvedValueOnce({ completed: 2, total: 2, succeeded: 2, failed: 0, skipped: 0 });
  await mountPage();
  vi.useFakeTimers({ toFake: ['setTimeout', 'clearTimeout'] });
  try {
    const probe = () => [...document.querySelectorAll<HTMLButtonElement>('button')]
      .find((button) => button.textContent?.trim() === t('probeAllModels'))!;
    const flash = () => document.querySelector('.oa-control-flash.ok')?.textContent ?? '';
    const second = t('probeAllModelsDone', { succeeded: 2, total: 2, failed: 0, skipped: 0 });

    probe().click();
    await vi.advanceTimersByTimeAsync(4000);
    probe().click();
    await vi.advanceTimersByTimeAsync(0);
    expect(probeAll).toHaveBeenCalledTimes(2);
    expect(flash()).toBe(second);

    // Past the first run's five seconds, inside the second's.
    await vi.advanceTimersByTimeAsync(2000);
    expect(flash()).toBe(second);

    await vi.advanceTimersByTimeAsync(3500);
    expect(flash()).toBe('');
  } finally {
    vi.useRealTimers();
  }
});

// The cooldown is a code the interface has a sentence for, so a Chinese
// screen says it in Chinese rather than in the server's English.
it('words a refused test run by its code, in the reader\'s language', async () => {
  await changeLanguage('zh');
  try {
    vi.spyOn(adminApi, 'probeAllModels').mockRejectedValue(
      new ApiError(429, 'probe_cooldown', 'All models can be tested once every thirty seconds.'));
    await mountPage();
    [...document.querySelectorAll<HTMLButtonElement>('button')]
      .find((button) => button.textContent?.trim() === t('probeAllModels'))!.click();
    await settle();

    const text = document.body.textContent ?? '';
    expect(text).toContain(t('errProbeCooldown'));
    expect(text).not.toContain('once every thirty seconds');
  } finally {
    await changeLanguage('en');
  }
});
