import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import { createApp, h, nextTick, type App } from 'vue';
import { createMemoryHistory, createRouter } from 'vue-router';
import { adminApi } from '../src/admin/api';
import { changeLanguage, t } from '../src/composables/useI18n';
import AdminSettings from '../src/views/admin/AdminSettings.vue';
import { provideAdminView } from '../src/views/admin/adminView';
import settingsFixture from './fixtures/settings.json';

let app: App | undefined;
let host: HTMLElement;
let actions: HTMLElement;
async function settle(): Promise<void> { await vi.advanceTimersByTimeAsync(0); await nextTick(); }
beforeEach(async () => {
  await changeLanguage('en');
  // A successful save leaves a delayed label reset; keep it inside this test.
  vi.useFakeTimers();
  host = document.createElement('div');
  actions = document.createElement('div');
  document.body.append(host, actions);
  vi.spyOn(adminApi, 'modelOptions').mockResolvedValue({ models: [] });
});
afterEach(() => {
  app?.unmount();
  app = undefined;
  document.body.textContent = '';
  vi.clearAllTimers();
  vi.useRealTimers();
  vi.restoreAllMocks();
});
function button(root: ParentNode, label: string): HTMLButtonElement {
  const found = [...root.querySelectorAll<HTMLButtonElement>('button')].find((node) => node.textContent?.trim() === label || node.getAttribute('aria-label') === label);
  if (!found) throw new Error(`Missing button: ${label}`);
  return found;
}
async function selectCategory(label: string): Promise<void> {
  const found = [...host.querySelectorAll<HTMLButtonElement>('.oa-workbench-tab')].find((node) => node.querySelector('strong')?.textContent === label);
  if (!found) throw new Error(`Missing category: ${label}`);
  found.click();
  await nextTick();
  expect(found.getAttribute('aria-pressed')).toBe('true');
}
function roundsInput(): HTMLInputElement {
  const field = [...host.querySelectorAll('#secChat label')].find((node) => node.querySelector('.oa-field-label')?.textContent === t('agentMaxRounds'));
  const input = field?.querySelector<HTMLInputElement>('input[type="number"]');
  if (!input) throw new Error('Missing agent rounds input');
  return input;
}
function expectSaveState(dirty: boolean): void {
  const state = actions.querySelector('.oa-control-save-state');
  expect(state?.textContent?.trim()).toBe(t(dirty ? 'controlUnsaved' : 'controlSaved'));
  expect(state?.classList.contains('dirty')).toBe(dirty);
}
async function mountSettings(): Promise<void> {
  const router = createRouter({ history: createMemoryHistory(), routes: [{ path: '/:pathMatch(.*)*', component: { render: () => null } }] });
  app = createApp({ setup() {
    provideAdminView({ actionsHost: actions, setTitle() {}, reload() {}, params: [] });
    return () => h(AdminSettings);
  } });
  app.use(router);
  app.mount(host);
  await settle();
}

describe('admin settings payload', () => {
  it('submits the complete shared payload, including hidden default rounds, when enabling API keys', async () => {
    vi.spyOn(adminApi, 'settings').mockResolvedValue({ settings: { ...settingsFixture, 'api.enabled': 'false' } });
    const save = vi.spyOn(adminApi, 'saveSettings').mockResolvedValue({ settings: { ...settingsFixture } });
    await mountSettings();
    expectSaveState(false);

    await selectCategory(t('controlIntegrations'));
    expect(host.querySelector<HTMLElement>('#apiKeys')?.style.display).not.toBe('none');
    expect(host.querySelector<HTMLElement>('#secChat')?.style.display).toBe('none');
    expect(roundsInput().value).toBe('8');
    const apiKeys = host.querySelector<HTMLInputElement>('#apiKeys input[type="checkbox"]')!;
    expect(apiKeys.checked).toBe(false);
    apiKeys.click();
    await nextTick();
    expect(apiKeys.checked).toBe(true);
    expectSaveState(true);

    button(actions, t('save')).click();
    await settle();
    expect(save).toHaveBeenCalledTimes(1);
    expect(save).toHaveBeenCalledWith(settingsFixture);
    expect(host.querySelector<HTMLElement>('#secChat')?.style.display).toBe('none');
    expectSaveState(false);
    expect(button(actions, t('saved')).disabled).toBe(false);
  });

  it.each([
    { label: 'edited custom rounds', input: '15', expected: '15' },
    { label: 'default rounds after clearing the field', input: '', expected: '8' },
  ])('preserves the complete payload with $label', async ({ input, expected }) => {
    vi.spyOn(adminApi, 'settings').mockResolvedValue({ settings: { ...settingsFixture, 'chat.agent_max_rounds': '12' } });
    const expectedSettings = { ...settingsFixture, 'chat.agent_max_rounds': expected };
    const save = vi.spyOn(adminApi, 'saveSettings').mockResolvedValue({ settings: expectedSettings });
    await mountSettings();
    expectSaveState(false);

    await selectCategory(t('controlChat'));
    expect(host.querySelector<HTMLElement>('#secChat')?.style.display).not.toBe('none');
    const rounds = roundsInput();
    expect(rounds.value).toBe('12');
    rounds.value = input;
    rounds.dispatchEvent(new Event('input', { bubbles: true }));
    await nextTick();
    expect(rounds.value).toBe(input);
    expectSaveState(true);

    button(actions, t('save')).click();
    await settle();
    expect(save).toHaveBeenCalledTimes(1);
    expect(save).toHaveBeenCalledWith(expectedSettings);
    expectSaveState(false);
    expect(button(actions, t('saved')).disabled).toBe(false);
  });

  it('updates auth card position setting', async () => {
    vi.spyOn(adminApi, 'settings').mockResolvedValue({ settings: { ...settingsFixture } });
    const expectedSettings = { ...settingsFixture, 'site.auth_card_position': 'left' };
    const save = vi.spyOn(adminApi, 'saveSettings').mockResolvedValue({ settings: expectedSettings });
    await mountSettings();
    expectSaveState(false);

    expect(host.querySelector<HTMLElement>('#secLoginBg')?.style.display).not.toBe('none');
    const selectTrigger = host.querySelector<HTMLButtonElement>('#secLoginBg .oa-select');
    expect(selectTrigger).not.toBeNull();
    expect(selectTrigger?.textContent).toContain(t('authCardPositionCenter'));

    selectTrigger!.click();
    await nextTick();
    const leftOption = [...document.querySelectorAll<HTMLElement>('[role="option"]')]
      .find((node) => node.textContent?.includes(t('authCardPositionLeft')));
    expect(leftOption).toBeDefined();
    leftOption!.click();
    await nextTick();
    expectSaveState(true);

    button(actions, t('save')).click();
    await settle();
    expect(save).toHaveBeenCalledTimes(1);
    expect(save).toHaveBeenCalledWith(expectedSettings);
    expectSaveState(false);
  });

  it('filters to secLoginBg when searching for auth card position keywords', async () => {
    vi.spyOn(adminApi, 'settings').mockResolvedValue({ settings: { ...settingsFixture } });
    await mountSettings();

    const searchInput = host.querySelector<HTMLInputElement>('.oa-search input');
    expect(searchInput).not.toBeNull();

    searchInput!.value = 'auth card position';
    searchInput!.dispatchEvent(new Event('input', { bubbles: true }));
    await nextTick();

    expect(host.querySelector<HTMLElement>('#secLoginBg')?.style.display).not.toBe('none');
    expect(host.querySelector<HTMLElement>('#secAbout')?.style.display).toBe('none');
  });

  it('opens confirmation modal when disabling showSoftwareInfo and respects user decision', async () => {
    vi.spyOn(adminApi, 'settings').mockResolvedValue({ settings: { ...settingsFixture } });
    await mountSettings();

    const aboutCard = host.querySelector('#secAbout');
    expect(aboutCard).not.toBeNull();
    const checkbox = aboutCard?.querySelector<HTMLInputElement>('.oa-switch-field input[type="checkbox"]');
    expect(checkbox).not.toBeNull();
    expect(checkbox!.checked).toBe(true);

    // Click checkbox to turn it OFF
    checkbox!.click();
    await nextTick();

    // Confirmation modal should now be visible in body
    const modal = document.body.querySelector('.oa-modal-card');
    expect(modal).not.toBeNull();
    expect(modal?.querySelector('.oa-auth-title')?.textContent).toBe(t('aboutDisableModalTitle'));

    // 1. Click "Keep Enabled" (cancel)
    const keepBtn = [...modal!.querySelectorAll<HTMLButtonElement>('button')].find(
      (b) => b.textContent?.trim() === t('aboutDisableModalKeep'),
    );
    expect(keepBtn).not.toBeUndefined();
    keepBtn!.click();
    await nextTick();

    // Modal is dismissed, setting remains true, form is not dirty
    expect(document.body.querySelector('.oa-modal-card')).toBeNull();
    expect(checkbox!.checked).toBe(true);
    expectSaveState(false);

    // 2. Click checkbox again to turn it OFF, then confirm
    checkbox!.click();
    await nextTick();

    const modal2 = document.body.querySelector('.oa-modal-card');
    expect(modal2).not.toBeNull();
    const confirmBtn = [...modal2!.querySelectorAll<HTMLButtonElement>('button')].find(
      (b) => b.textContent?.trim() === t('aboutDisableModalConfirm'),
    );
    expect(confirmBtn).not.toBeUndefined();
    confirmBtn!.click();
    await nextTick();

    // Modal is dismissed, setting is now false, form is dirty
    expect(document.body.querySelector('.oa-modal-card')).toBeNull();
    expect(checkbox!.checked).toBe(false);
    expectSaveState(true);
  });
});
