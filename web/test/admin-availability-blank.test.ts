// A blank number box on the availability page is not a value. Saving used to
// store the default in its place while the box stayed blank on screen, so the
// page showed one figure and kept another. Now the save stays shut until a
// number is typed, and only the number that was typed is sent.

import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import { createApp, h, type App } from 'vue';
import { createMemoryHistory, createRouter } from 'vue-router';
import { adminApi } from '../src/admin/api';
import { changeLanguage, t } from '../src/composables/useI18n';
import AdminAvailability from '../src/views/admin/AdminAvailability.vue';
import { provideAdminView } from '../src/views/admin/adminView';
import { settle } from './helpers';

let app: App | undefined;
let host: HTMLElement;
let actions: HTMLElement;


function numberField(label: string): HTMLInputElement {
  const field = [...host.querySelectorAll<HTMLElement>('.oa-field')]
    .find((node) => node.querySelector('.oa-field-label')?.textContent?.trim() === label);
  const input = field?.querySelector<HTMLInputElement>('input');
  if (!input) throw new Error(`Missing field: ${label}`);
  return input;
}

function type(input: HTMLInputElement, value: string): void {
  input.value = value;
  input.dispatchEvent(new Event('input', { bubbles: true }));
}

function saveButton(): HTMLButtonElement {
  const found = actions.querySelector<HTMLButtonElement>('button.primary');
  if (!found) throw new Error('Missing save button');
  return found;
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
  vi.spyOn(adminApi, 'saveSettings').mockResolvedValue(undefined as never);
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

describe('a blank number on the availability page', () => {
  it('keeps the save shut and says why, instead of saving a default in its place', async () => {
    await mountPage();

    type(numberField(t('healthWindow')), '');
    await settle();

    expect(saveButton().disabled).toBe(true);
    expect(host.textContent).toContain(t('settingNumberRequired'));
    saveButton().click();
    await settle();
    expect(adminApi.saveSettings).not.toHaveBeenCalled();
  });

  it('saves the number typed in its place, and only that setting', async () => {
    await mountPage();

    type(numberField(t('healthWindow')), '');
    await settle();
    type(numberField(t('healthWindow')), '45');
    await settle();

    expect(saveButton().disabled).toBe(false);
    saveButton().click();
    await settle();
    expect(adminApi.saveSettings).toHaveBeenCalledWith({ 'health.window_minutes': '45' });
  });

  // One check covers every number on the page. A box that dropped out of it
  // would save the default it stood for again, so each one gets its own case.
  it.each(['healthDisableAfter', 'healthDisableBelow', 'healthWarnBelow', 'healthRetainDays'] as const)(
    'keeps the save shut when %s is left blank',
    async (key) => {
      await mountPage();

      type(numberField(t(key)), '');
      await settle();

      expect(saveButton().disabled).toBe(true);
      expect(host.textContent).toContain(t('settingNumberRequired'));
    },
  );
});
