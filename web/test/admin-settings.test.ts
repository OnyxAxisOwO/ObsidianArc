import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import { createApp, h, nextTick, type App } from 'vue';
import { createMemoryHistory, createRouter } from 'vue-router';
import { adminApi } from '../src/admin/api';
import { changeLanguage, t } from '../src/composables/useI18n';
import AdminSettings from '../src/views/admin/AdminSettings.vue';
import { provideAdminView } from '../src/views/admin/adminView';
import settingsFixture from './fixtures/settings.json';
import { settle } from './helpers';

let app: App | undefined;
let host: HTMLElement;
let actions: HTMLElement;
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
function turnsInput(): HTMLInputElement {
  const field = [...host.querySelectorAll('#secChat label')].find((node) => node.querySelector('.oa-field-label')?.textContent === t('turnsResent'));
  const input = field?.querySelector<HTMLInputElement>('input[type="number"]');
  if (!input) throw new Error('Missing resent turns input');
  return input;
}
function expectSaveState(dirty: boolean): void {
  const state = actions.querySelector('.oa-control-save-state');
  expect(state?.textContent?.trim()).toBe(t(dirty ? 'controlUnsaved' : 'controlSaved'));
  expect(state?.classList.contains('dirty')).toBe(dirty);
}
async function mountSettings(onError?: (error: unknown) => void): Promise<void> {
  const router = createRouter({ history: createMemoryHistory(), routes: [{ path: '/:pathMatch(.*)*', component: { render: () => null } }] });
  app = createApp({ setup() {
    provideAdminView({ actionsHost: actions, setTitle() {}, reload() {}, params: [] });
    return () => h(AdminSettings);
  } });
  // A render error goes here instead of being thrown, so the test can read it.
  if (onError) app.config.errorHandler = onError;
  app.use(router);
  app.mount(host);
  await settle();
}
/** A drop onto the logo preview, carrying one file, as the browser would deliver it. */
function dropFile(target: HTMLElement, file: File): void {
  const event = new Event('drop', { bubbles: true, cancelable: true });
  Object.defineProperty(event, 'dataTransfer', { value: { files: [file] } });
  target.dispatchEvent(event);
}
/** Captures the file input the next picker creates, so the test can choose a file in it. */
function captureInput(): () => HTMLInputElement {
  const real = document.createElement.bind(document);
  let input: HTMLInputElement | null = null;
  vi.spyOn(document, 'createElement').mockImplementation((tag: string, options?: ElementCreationOptions) => {
    const element = real(tag, options);
    if (tag === 'input') input = element as HTMLInputElement;
    return element;
  });
  return () => {
    if (!input) throw new Error('no picker was created');
    return input;
  };
}

describe('admin settings payload', () => {
  it('submits only the key it changed when enabling API keys', async () => {
    vi.spyOn(adminApi, 'settings').mockResolvedValue({ settings: { ...settingsFixture, 'api.enabled': 'false' } });
    const save = vi.spyOn(adminApi, 'saveSettings').mockResolvedValue({ settings: { ...settingsFixture } });
    await mountSettings();
    expectSaveState(false);

    await selectCategory(t('controlIntegrations'));
    expect(host.querySelector<HTMLElement>('#apiKeys')?.style.display).not.toBe('none');
    expect(host.querySelector<HTMLElement>('#secChat')?.style.display).toBe('none');
    expect(turnsInput().value).toBe('40');
    const apiKeys = host.querySelector<HTMLInputElement>('#apiKeys input[type="checkbox"]')!;
    expect(apiKeys.checked).toBe(false);
    apiKeys.click();
    await nextTick();
    expect(apiKeys.checked).toBe(true);
    expectSaveState(true);

    button(actions, t('save')).click();
    await settle();
    expect(save).toHaveBeenCalledTimes(1);
    expect(save).toHaveBeenCalledWith({ 'api.enabled': 'true' });
    expect(host.querySelector<HTMLElement>('#secChat')?.style.display).toBe('none');
    expectSaveState(false);
    expect(button(actions, t('saved')).disabled).toBe(false);
  });

  it.each([
    { label: 'an edited value', input: '15', expected: '15' },
    { label: 'the default after clearing the field', input: '', expected: '40' },
  ])('submits only the edited field with $label', async ({ input, expected }) => {
    vi.spyOn(adminApi, 'settings').mockResolvedValue({ settings: { ...settingsFixture, 'chat.max_turns': '12' } });
    const save = vi.spyOn(adminApi, 'saveSettings').mockResolvedValue({ settings: { ...settingsFixture, 'chat.max_turns': expected } });
    await mountSettings();
    expectSaveState(false);

    await selectCategory(t('controlChat'));
    expect(host.querySelector<HTMLElement>('#secChat')?.style.display).not.toBe('none');
    const turns = turnsInput();
    expect(turns.value).toBe('12');
    turns.value = input;
    turns.dispatchEvent(new Event('input', { bubbles: true }));
    await nextTick();
    expect(turns.value).toBe(input);
    expectSaveState(true);

    button(actions, t('save')).click();
    await settle();
    expect(save).toHaveBeenCalledTimes(1);
    expect(save).toHaveBeenCalledWith({ 'chat.max_turns': expected });
    expectSaveState(false);
    expect(button(actions, t('saved')).disabled).toBe(false);
  });

  it('submits the archive switch when an administrator turns it off', async () => {
    vi.spyOn(adminApi, 'settings').mockResolvedValue({ settings: { ...settingsFixture } });
    const save = vi.spyOn(adminApi, 'saveSettings').mockResolvedValue({ settings: { ...settingsFixture, 'chat.allow_archive': 'false' } });
    await mountSettings();
    expectSaveState(false);

    await selectCategory(t('controlChat'));
    const field = [...host.querySelectorAll<HTMLElement>('#secChat .oa-switch-field')].find((node) => node.textContent?.includes(t('chatAllowArchive')));
    const archive = field?.querySelector<HTMLInputElement>('input[type="checkbox"]');
    if (!archive) throw new Error('Missing archive switch');
    expect(archive.checked).toBe(true);
    archive.click();
    await nextTick();
    expect(archive.checked).toBe(false);
    expectSaveState(true);

    button(actions, t('save')).click();
    await settle();
    expect(save).toHaveBeenCalledTimes(1);
    expect(save).toHaveBeenCalledWith({ 'chat.allow_archive': 'false' });
    expectSaveState(false);
  });

  it('updates auth card position setting', async () => {
    vi.spyOn(adminApi, 'settings').mockResolvedValue({ settings: { ...settingsFixture } });
    const save = vi.spyOn(adminApi, 'saveSettings').mockResolvedValue({ settings: { ...settingsFixture, 'site.auth_card_position': 'left' } });
    await mountSettings();
    expectSaveState(false);

    expect(host.querySelector<HTMLElement>('#secLoginBg')?.style.display).not.toBe('none');
    // A segmented control now, not a select: three words fit on one line.
    const choices = [...host.querySelectorAll<HTMLButtonElement>('#secLoginBg .oa-segment button')];
    const center = choices.find((node) => node.textContent?.includes(t('authCardPositionCenter')));
    expect(center?.getAttribute('aria-pressed')).toBe('true');
    const left = choices.find((node) => node.textContent?.includes(t('authCardPositionLeft')));
    expect(left).toBeDefined();
    left!.click();
    await nextTick();
    expect(left!.getAttribute('aria-pressed')).toBe('true');
    expectSaveState(true);

    button(actions, t('save')).click();
    await settle();
    expect(save).toHaveBeenCalledTimes(1);
    expect(save).toHaveBeenCalledWith({ 'site.auth_card_position': 'left' });
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

describe('the site theme card', () => {
  // The server accepts any short lowercase accent name, and "constructor" is
  // one that every object inherits. It must read as an unknown accent, not
  // take the whole card down.
  it('draws when the stored accent is a name every object inherits', async () => {
    const errors: unknown[] = [];
    vi.spyOn(adminApi, 'settings').mockResolvedValue({ settings: { ...settingsFixture, 'theme.accent': 'constructor' } });
    await mountSettings((error) => errors.push(error));

    expect(errors).toEqual([]);
    const card = host.querySelector<HTMLElement>('#secSiteTheme');
    expect(card?.querySelectorAll('.oa-site-tints .oa-background-choice')).toHaveLength(6);
    expect(card?.textContent).not.toContain('native code');
  });
});

describe('a retry after a failed first load', () => {
  // The baseline is what makes Save meaningful. Until the reload lands there is
  // none, so every default would read as an edit and be written back.
  it('keeps Save disabled until the reload has a baseline', async () => {
    let finishReload: (value: { settings: Record<string, string> }) => void = () => {};
    const reloaded = new Promise<{ settings: Record<string, string> }>((resolve) => { finishReload = resolve; });
    vi.spyOn(adminApi, 'settings')
      .mockRejectedValueOnce(new Error('network down'))
      .mockReturnValueOnce(reloaded);
    const save = vi.spyOn(adminApi, 'saveSettings').mockResolvedValue({ settings: { ...settingsFixture } });
    await mountSettings();
    expect(host.textContent).toContain(t('couldNotLoad'));

    button(host, t('tryAgain')).click();
    await settle();
    expect(button(actions, t('save')).disabled).toBe(true);
    expect(actions.querySelector('.oa-control-save-state')).toBeNull();

    finishReload({ settings: { ...settingsFixture } });
    await settle();
    expect(button(actions, t('save')).disabled).toBe(false);
    expectSaveState(false);
    expect(save).not.toHaveBeenCalled();
  });
});

describe('a value past the server limit', () => {
  // The server refuses the whole save over one value this long, so the other
  // changes on the form would fail with it. The form says so and does not send.
  it('shows the overrun and refuses to save it', async () => {
    vi.spyOn(adminApi, 'settings').mockResolvedValue({ settings: { ...settingsFixture, 'landing.mode': 'intro' } });
    const save = vi.spyOn(adminApi, 'saveSettings').mockResolvedValue({ settings: { ...settingsFixture } });
    await mountSettings();
    expectSaveState(false);

    const intro = [...host.querySelectorAll<HTMLLabelElement>('#secLanding label.oa-field')]
      .find((label) => label.querySelector('.oa-field-label')?.textContent === t('landingIntroHTML'));
    const area = intro?.querySelector<HTMLTextAreaElement>('textarea');
    expect(area).toBeTruthy();
    // Three bytes a character: 9,000 bytes, past the 8 KiB the server takes.
    area!.value = '\u3042'.repeat(3000);
    area!.dispatchEvent(new Event('input', { bubbles: true }));
    await nextTick();

    expect(intro!.textContent).toContain(t('fieldByteCountOver', { used: 9000, limit: 8192 }));
    expect(area!.getAttribute('aria-invalid')).toBe('true');

    button(actions, t('save')).click();
    await settle();
    expect(save).not.toHaveBeenCalled();
    expect(host.querySelector('.oa-control-flash')?.textContent).toBe(t('settingTooLong'));
  });
});

describe('the site logo', () => {
  // The preview takes clicks and drops while an upload is still in flight. A
  // second upload then races the first, and whichever response arrives last
  // decides the image on the page, which need not be the one the server kept.
  it('does not start a second upload while one is in flight', async () => {
    vi.spyOn(adminApi, 'settings').mockResolvedValue({ settings: { ...settingsFixture } });
    let finishUpload: (value: { url: string; updated_at: number }) => void = () => {};
    const upload = vi.spyOn(adminApi, 'uploadSiteLogo').mockReturnValue(
      new Promise((resolve) => { finishUpload = resolve; }),
    );
    await mountSettings();

    const preview = host.querySelector<HTMLElement>('.oa-logo-preview')!;
    dropFile(preview, new File(['first'], 'first.png', { type: 'image/png' }));
    dropFile(preview, new File(['second'], 'second.png', { type: 'image/png' }));
    // The first drop reads its file asynchronously, and that read has to finish
    // before the uploads are counted. The second drop returns at the guard
    // without reading anything.
    await vi.waitFor(() => expect(upload).toHaveBeenCalled());
    await vi.advanceTimersByTimeAsync(1000);
    await nextTick();
    expect(upload).toHaveBeenCalledTimes(1);

    finishUpload({ url: '/logo-first.png', updated_at: 1 });
    await settle();
    expect(preview.querySelector('img')?.getAttribute('src')).toBe('/logo-first.png');
    expect(button(host, t('siteLogoReplace')).disabled).toBe(false);
  });
});

describe('importing a settings file', () => {
  // The import endpoint reads at most 256 KiB. The picker used to accept four
  // times that and send the file anyway, where it came back as a generic
  // decode failure; now it refuses it with the message a too-large file gets.
  it('refuses a file the endpoint would not read', async () => {
    vi.spyOn(adminApi, 'settings').mockResolvedValue({ settings: { ...settingsFixture } });
    const importer = vi.spyOn(adminApi, 'importSettings').mockResolvedValue({ settings: {}, applied: 0, skipped: [] });
    await mountSettings();

    const picker = captureInput();
    button(host, t('importSettings')).click();
    const file = new File([JSON.stringify({ 'site.name': 'a'.repeat(300 * 1024) })], 'settings.json', { type: 'application/json' });
    Object.defineProperty(picker(), 'files', { value: [file], configurable: true });
    picker().dispatchEvent(new Event('change'));

    await vi.waitFor(() => expect(host.querySelector('.oa-control-flash')?.textContent).toBe(t('importFileTooLarge')));
    expect(importer).not.toHaveBeenCalled();
  });
});
