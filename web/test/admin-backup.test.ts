import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import { createApp, h, nextTick, type App } from 'vue';
import { adminApi, type AdminBackup } from '../src/admin/api';
import { changeLanguage, t } from '../src/composables/useI18n';
import { IconArchive } from '../src/icons';
import { searchAdminFeatures, visibleAdminPages, type AdminPageSpec } from '../src/views/admin/features';
import { provideAdminView } from '../src/views/admin/adminView';
import AdminBackupPage from '../src/views/admin/AdminBackup.vue';

let app: App | undefined;
let host: HTMLElement;
let actions: HTMLElement;
let visibilityDescriptor: PropertyDescriptor | undefined;

const backup: AdminBackup = {
  enabled: true,
  configured: true,
  endpoint: 'https://storage.example.com',
  bucket: 'arc-backups',
  region: 'auto',
  prefix: 'production',
  interval_hours: 24,
  retention_days: 7,
  secret_configured: true,
  running: false,
  last_status: 'success',
  last_started_at: Date.UTC(2026, 8, 27, 1),
  last_finished_at: Date.UTC(2026, 8, 27, 1, 1),
  last_success_at: Date.UTC(2026, 8, 27, 1, 1),
  next_run_at: Date.UTC(2026, 8, 28, 1, 1),
  last_error: '',
};

beforeEach(async () => {
  await changeLanguage('en');
  visibilityDescriptor = Object.getOwnPropertyDescriptor(document, 'visibilityState');
  host = document.createElement('div');
  actions = document.createElement('div');
  document.body.append(host, actions);
});

afterEach(() => {
  app?.unmount();
  app = undefined;
  document.body.textContent = '';
  if (visibilityDescriptor) Object.defineProperty(document, 'visibilityState', visibilityDescriptor);
  else Reflect.deleteProperty(document, 'visibilityState');
  vi.restoreAllMocks();
  vi.useRealTimers();
});

async function settle(): Promise<void> {
  await Promise.resolve();
  await nextTick();
  await Promise.resolve();
  await nextTick();
}

async function mountBackup(): Promise<void> {
  app = createApp({ setup() {
    provideAdminView({ actionsHost: actions, setTitle() {}, reload() {}, params: [] });
    return () => h(AdminBackupPage);
  } });
  app.mount(host);
  await settle();
}

function fieldInput(label: string): HTMLInputElement {
  const field = [...host.querySelectorAll<HTMLLabelElement>('label.oa-field')]
    .find((node) => node.querySelector('.oa-field-label')?.textContent === label);
  const input = field?.querySelector<HTMLInputElement>('input');
  if (!input) throw new Error(`Missing field: ${label}`);
  return input;
}

function button(root: ParentNode, label: string): HTMLButtonElement {
  const found = [...root.querySelectorAll<HTMLButtonElement>('button')]
    .find((node) => node.textContent?.trim() === label);
  if (!found) throw new Error(`Missing button: ${label}`);
  return found;
}

describe('admin instance backup', () => {
  it('keeps the instance-wide page out of delegated navigation and search', async () => {
    await changeLanguage('en');
    const pages: AdminPageSpec[] = [
      { slug: 'settings', label: 'navSettings', icon: IconArchive, component: AdminBackupPage },
      { slug: 'backup', label: 'navBackup', icon: IconArchive, component: AdminBackupPage, permission: '*' },
    ];

    const delegated = visibleAdminPages(pages, false);
    expect(delegated.map((page) => page.slug)).toEqual(['settings']);
    expect(searchAdminFeatures('instance backup', delegated)).toEqual([]);
    expect(visibleAdminPages(pages, true).map((page) => page.slug)).toContain('backup');
  });

  it('keeps stored credentials blank and submits blank fields to preserve them', async () => {
    vi.spyOn(adminApi, 'backup').mockResolvedValue(backup);
    const save = vi.spyOn(adminApi, 'saveBackup').mockResolvedValue(undefined);
    await mountBackup();

    expect(fieldInput(t('backupAccessKey')).value).toBe('');
    expect(fieldInput(t('backupSecretKey')).value).toBe('');

    const prefix = fieldInput(t('backupPrefix'));
    prefix.value = 'weekly';
    prefix.dispatchEvent(new Event('input', { bubbles: true }));
    await nextTick();
    button(actions, t('save')).click();
    await settle();

    expect(save).toHaveBeenCalledWith({
      enabled: true,
      endpoint: backup.endpoint,
      bucket: backup.bucket,
      region: backup.region,
      prefix: 'weekly',
      access_key_id: '',
      secret_access_key: '',
      interval_hours: backup.interval_hours,
      retention_days: backup.retention_days,
    });
    expect(fieldInput(t('backupAccessKey')).value).toBe('');
    expect(fieldInput(t('backupSecretKey')).value).toBe('');
  });

  it('shows Never for timestamps that have no recorded run', async () => {
    vi.spyOn(adminApi, 'backup').mockResolvedValue({
      ...backup,
      last_started_at: 0,
      last_finished_at: 0,
      last_success_at: 0,
    });
    await mountBackup();

    for (const key of ['backupLastStarted', 'backupLastFinished', 'backupLastSuccess'] as const) {
      const label = [...host.querySelectorAll('dt')].find((node) => node.textContent === t(key));
      expect(label?.parentElement?.querySelector('dd')?.textContent?.trim()).toBe(t('backupNever'));
    }
  });

  it('tests storage and refreshes a running backup until its status settles', async () => {
    vi.useFakeTimers();
    Object.defineProperty(document, 'visibilityState', { configurable: true, value: 'visible' });
    const running = { ...backup, running: true, last_status: 'running' as const };
    const finished = { ...backup, last_started_at: Date.now(), last_finished_at: Date.now(), last_success_at: Date.now() };
    vi.spyOn(adminApi, 'backup')
      .mockResolvedValueOnce(backup)
      .mockResolvedValueOnce(backup)
      .mockResolvedValueOnce(running)
      .mockResolvedValueOnce(finished);
    const test = vi.spyOn(adminApi, 'testBackup').mockResolvedValue({ ok: true });
    const run = vi.spyOn(adminApi, 'runBackup').mockResolvedValue({ ok: true, running: true });
    await mountBackup();

    button(host, t('backupTest')).click();
    await settle();
    expect(test).toHaveBeenCalledOnce();
    expect(host.textContent).toContain(t('backupTestSucceeded'));

    button(host, t('backupRun')).click();
    await settle();
    expect(run).toHaveBeenCalledOnce();
    expect(host.textContent).toContain(t('backupStatusRunning'));

    await vi.advanceTimersByTimeAsync(5000);
    await settle();
    expect(host.textContent).toContain(t('backupStatusSuccess'));
  });

  it('only shows save hint when configuration is dirty, clearing it on save', async () => {
    vi.spyOn(adminApi, 'backup').mockResolvedValue(backup);
    vi.spyOn(adminApi, 'saveBackup').mockResolvedValue(undefined);
    await mountBackup();

    const testBtn = button(host, t('backupTest'));
    const runBtn = button(host, t('backupRun'));
    expect(testBtn.disabled).toBe(false);
    expect(runBtn.disabled).toBe(false);
    expect(host.textContent).not.toContain(t('backupActionsHint'));

    const prefix = fieldInput(t('backupPrefix'));
    prefix.value = 'changed-prefix';
    prefix.dispatchEvent(new Event('input', { bubbles: true }));
    await nextTick();

    expect(testBtn.disabled).toBe(true);
    expect(runBtn.disabled).toBe(true);
    expect(host.textContent).toContain(t('backupActionsHint'));

    button(actions, t('save')).click();
    await settle();

    expect(testBtn.disabled).toBe(false);
    expect(runBtn.disabled).toBe(false);
    expect(host.textContent).not.toContain(t('backupActionsHint'));
    expect(host.textContent).toContain(t('backupSaved'));

    // Editing a field after save should clear the saved notice so it does not
    // contradict the newly displayed save hint.
    prefix.value = 'changed-again';
    prefix.dispatchEvent(new Event('input', { bubbles: true }));
    await nextTick();

    expect(testBtn.disabled).toBe(true);
    expect(runBtn.disabled).toBe(true);
    expect(host.textContent).toContain(t('backupActionsHint'));
    expect(host.textContent).not.toContain(t('backupSaved'));
  });

  it('shows storage missing hint instead of save hint when unconfigured and clean', async () => {
    vi.spyOn(adminApi, 'backup').mockResolvedValue({
      ...backup,
      configured: false,
      secret_configured: false,
      endpoint: '',
      bucket: '',
    });
    await mountBackup();

    const testBtn = button(host, t('backupTest'));
    const runBtn = button(host, t('backupRun'));
    expect(testBtn.disabled).toBe(true);
    expect(runBtn.disabled).toBe(true);
    expect(host.textContent).not.toContain(t('backupActionsHint'));
    expect(host.textContent).toContain(t('backupStorageMissing'));
  });

  it('preserves action error across subsequent successful background polling', async () => {
    vi.useFakeTimers();
    Object.defineProperty(document, 'visibilityState', { configurable: true, value: 'visible' });
    vi.spyOn(adminApi, 'backup').mockResolvedValue(backup);
    vi.spyOn(adminApi, 'testBackup').mockRejectedValue(new Error('connection timeout'));
    await mountBackup();

    button(host, t('backupTest')).click();
    await settle();
    expect(host.textContent).toContain('connection timeout');

    // Automatic background poll runs 30s later and resolves successfully.
    // It must not erase the test error that the operator is inspecting.
    await vi.advanceTimersByTimeAsync(30000);
    await settle();
    expect(host.textContent).toContain('connection timeout');
  });

  it('correctly updates configured status when saving while background poll was in flight', async () => {
    const unconfigured: AdminBackup = {
      ...backup,
      configured: false,
      secret_configured: false,
      endpoint: '',
      bucket: '',
    };
    const configured: AdminBackup = {
      ...backup,
      configured: true,
      secret_configured: true,
      endpoint: 'https://storage.example.com',
      bucket: 'arc-backups',
    };

    let resolvePoll: ((val: AdminBackup) => void) | undefined;
    const pollPromise = new Promise<AdminBackup>((resolve) => {
      resolvePoll = resolve;
    });

    // First call is mount load, second call is background poll (stuck in flight),
    // third call is save refresh.
    vi.spyOn(adminApi, 'backup')
      .mockResolvedValueOnce(unconfigured)
      .mockReturnValueOnce(pollPromise)
      .mockResolvedValueOnce(configured);
    vi.spyOn(adminApi, 'saveBackup').mockResolvedValue(undefined);

    await mountBackup();

    const testBtn = button(host, t('backupTest'));
    const runBtn = button(host, t('backupRun'));
    expect(testBtn.disabled).toBe(true);
    expect(runBtn.disabled).toBe(true);

    // Trigger in-flight poll
    void adminApi.backup();

    // Fill form and save
    const endpoint = fieldInput(t('backupEndpoint'));
    endpoint.value = 'https://storage.example.com';
    endpoint.dispatchEvent(new Event('input', { bubbles: true }));
    const bucketField = fieldInput(t('backupBucket'));
    bucketField.value = 'arc-backups';
    bucketField.dispatchEvent(new Event('input', { bubbles: true }));
    await nextTick();

    const savePromise = (async () => {
      button(actions, t('save')).click();
      await settle();
    })();

    // Background poll finishes before save refresh returns
    resolvePoll!(unconfigured);
    await savePromise;

    expect(testBtn.disabled).toBe(false);
    expect(runBtn.disabled).toBe(false);
    expect(host.textContent).not.toContain(t('backupActionsHint'));
    expect(host.textContent).not.toContain(t('backupStorageMissing'));
  });
});
