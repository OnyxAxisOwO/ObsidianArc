// The account panel's three commitments that go wrong quietly: which password
// form an account is shown (it is not known until the connections load), what
// a half-finished import says about what it already wrote, and that an export
// is saved in the form the importer will accept.

import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import { createApp, h, shallowRef, type App, type Component } from 'vue';
import * as authApi from '../src/api/auth';
import * as backupApi from '../src/api/backup';
import { ApiError } from '../src/api/client';
import * as consentApi from '../src/api/consent';
import * as oauthApi from '../src/api/oauth';
import type { Account } from '../src/api/auth';
import { providePanelHost } from '../src/composables/usePanelHost';
import { changeLanguage, t } from '../src/composables/useI18n';
import { adopt, forget } from '../src/stores/session';
import AccountSection from '../src/views/settings/AccountSection.vue';
import { settle } from './helpers';

const route = { path: '/settings', fullPath: '/settings', query: {} as Record<string, string>, hash: '', meta: {} };
const router = { replace: vi.fn() };
vi.mock('vue-router', () => ({
  useRoute: () => route,
  useRouter: () => router,
}));

const ACCOUNT: Account = {
  id: 'member', username: 'member', email: 'member@example.test', nickname: '', avatar: '', bio: '', role: 'user',
  group_id: '', group_expires_at: 0, group_name: '', status: 'active', created_at: 0, updated_at: 0,
  last_login_at: 0, email_verified: true, allow_stats: true, allow_delete_conversations: true,
  api_restricted: false, api_restricted_until: 0, api_restriction_source: '',
};

let app: App | undefined;
let host: HTMLElement;

beforeEach(async () => {
  await changeLanguage('en');
  route.query = {};
  router.replace.mockClear();
  adopt(ACCOUNT);
  vi.spyOn(consentApi, 'fetchAuthorizations').mockResolvedValue({ authorizations: [] } as never);
  host = document.createElement('div');
  document.body.append(host);
});

afterEach(() => {
  app?.unmount();
  app = undefined;
  document.body.textContent = '';
  vi.restoreAllMocks();
  forget();
});

async function mount(component: Component): Promise<void> {
  const panels = document.createElement('div');
  document.body.append(panels);
  app = createApp({
    setup() {
      providePanelHost(shallowRef(panels));
      return () => h(component, { query: '' });
    },
  });
  app.mount(host);
  await settle();
}

function button(label: string): HTMLButtonElement {
  const found = [...host.querySelectorAll<HTMLButtonElement>('button')]
    .find((node) => node.textContent?.trim() === label);
  if (!found) throw new Error(`Button not found: ${label}`);
  return found;
}

/** Every password field on the panel: the current password an address change asks for, and the password group's. */
function passwordInputs(): HTMLInputElement[] {
  return [...host.querySelectorAll<HTMLInputElement>('input[type="password"]')];
}

function emailInput(): HTMLInputElement {
  return host.querySelector<HTMLInputElement>('input[type="email"]')!;
}

function type(node: HTMLInputElement, value: string): void {
  node.value = value;
  node.dispatchEvent(new Event('input', { bubbles: true }));
}

describe('the password section', () => {
  it('shows neither password form while the connections have not loaded', async () => {
    vi.spyOn(oauthApi, 'fetchConnections').mockRejectedValue(new ApiError(500, 'internal', 'Could not reach the server.'));
    await mount(AccountSection);

    expect(passwordInputs()).toHaveLength(0);
    // A server failure's sentence was written for logs: the reader gets the
    // screen's own line in their language.
    expect(host.textContent).not.toContain('Could not reach the server.');
    expect(host.textContent).toContain(t('failed'));
    expect(button(t('retry'))).toBeTruthy();
  });

  it('shows the form the account actually has once a retry succeeds', async () => {
    const fetchConnections = vi.spyOn(oauthApi, 'fetchConnections')
      .mockRejectedValueOnce(new ApiError(500, 'internal', 'Could not reach the server.'))
      .mockResolvedValueOnce({ connections: [], providers: [], has_password: false } as never);
    await mount(AccountSection);
    button(t('retry')).click();
    await settle();

    expect(fetchConnections).toHaveBeenCalledTimes(2);
    expect(host.textContent).toContain(t('setPassword'));
    // An account with no password is given one field to set it with, not two.
    expect(passwordInputs()).toHaveLength(1);
    // The failure the retry replaces is taken off both screens with it.
    expect(host.textContent).not.toContain('Could not reach the server.');
  });

  it('asks for the current password on an address change while the connections have not loaded', async () => {
    vi.spyOn(oauthApi, 'fetchConnections').mockRejectedValue(new ApiError(500, 'internal', 'Could not reach the server.'));
    await mount(AccountSection);
    expect(passwordInputs()).toHaveLength(0);

    type(emailInput(), 'moved@example.test');
    await settle();

    // The server asks for the password whenever the account has one, so the
    // field cannot wait for the connections to say which kind of account this is.
    expect(host.textContent).toContain(t('emailChangePasswordHint'));
    expect(passwordInputs()).toHaveLength(1);
  });

  it('does not ask for the current password on an address change once the account is known to have none', async () => {
    vi.spyOn(oauthApi, 'fetchConnections').mockResolvedValue({ connections: [], providers: [], has_password: false } as never);
    await mount(AccountSection);

    type(emailInput(), 'moved@example.test');
    await settle();

    expect(host.textContent).not.toContain(t('emailChangePasswordHint'));
  });

  it('says a wrong current password in the page language, not the server sentence', async () => {
    vi.spyOn(oauthApi, 'fetchConnections').mockResolvedValue({ connections: [], providers: [], has_password: true } as never);
    vi.spyOn(authApi, 'changePassword').mockRejectedValue(
      new ApiError(400, 'current_password_wrong', 'Current password is incorrect.'),
    );
    await mount(AccountSection);

    type(passwordInputs()[0]!, 'not-the-password');
    type(passwordInputs()[1]!, 'a-better-password');
    button(t('changePassword')).click();
    await settle();

    expect(host.textContent).toContain(t('currentPasswordWrong'));
    expect(host.textContent).not.toContain('Current password is incorrect.');
  });
});

describe('the data section', () => {
  it('says how much a full account let in, rather than a generic failure', async () => {
    vi.spyOn(oauthApi, 'fetchConnections').mockResolvedValue({ connections: [], providers: [], has_password: true } as never);
    vi.spyOn(backupApi, 'pickJSONFile').mockResolvedValue({ obsidian_arc_export: 1, conversations: [] });
    vi.spyOn(backupApi, 'importAccount').mockRejectedValue(new ApiError(409, 'storage_full', 'This account is full.', {
      conversations: 3, messages: 41, skipped: 0, preferences: false,
    }));
    await mount(AccountSection);

    button(t('importData')).click();
    await settle();

    expect(host.textContent).toContain(t('importStorageFull', { conversations: 3, messages: 41 }));
    expect(host.textContent).not.toContain('This account is full.');
  });

  it('says a full account is full, not that an import stopped part way, when the import wrote nothing', async () => {
    vi.spyOn(oauthApi, 'fetchConnections').mockResolvedValue({ connections: [], providers: [], has_password: true } as never);
    vi.spyOn(backupApi, 'pickJSONFile').mockResolvedValue({ obsidian_arc_export: 1, conversations: [] });
    vi.spyOn(backupApi, 'importAccount').mockRejectedValue(new ApiError(409, 'storage_full', 'This account is full.', {
      conversations: 0, messages: 0, skipped: 0, preferences: false,
    }));
    await mount(AccountSection);

    button(t('importData')).click();
    await settle();

    expect(host.textContent).toContain(t('errStorageFull'));
    expect(host.textContent).not.toContain(t('importStorageFull', { conversations: 0, messages: 0 }));
  });

  it('says what a failure part way through an import wrote, whatever its code', async () => {
    vi.spyOn(oauthApi, 'fetchConnections').mockResolvedValue({ connections: [], providers: [], has_password: true } as never);
    vi.spyOn(backupApi, 'pickJSONFile').mockResolvedValue({ obsidian_arc_export: 1, conversations: [] });
    vi.spyOn(backupApi, 'importAccount').mockRejectedValue(new ApiError(500, 'internal', 'Something went wrong on our side.', {
      conversations: 2, messages: 9, skipped: 0, preferences: false,
    }));
    await mount(AccountSection);

    button(t('importData')).click();
    await settle();

    expect(host.textContent).toContain(t('importPartialFailed', { conversations: 2, messages: 9 }));
    expect(host.textContent).not.toContain('Something went wrong on our side.');
  });

  it('saves an export compact, the form the importer accepts', async () => {
    vi.spyOn(oauthApi, 'fetchConnections').mockResolvedValue({ connections: [], providers: [], has_password: true } as never);
    const backup: backupApi.BackupDocument = { obsidian_arc_export: 1, exported_at: 0, conversations: [] };
    vi.spyOn(backupApi, 'exportAccount').mockResolvedValue(backup);
    const save = vi.spyOn(backupApi, 'saveAsFile').mockImplementation(() => undefined);
    await mount(AccountSection);

    button(t('exportData')).click();
    await settle();

    expect(save).toHaveBeenCalledTimes(1);
    expect(save.mock.calls[0]![1]).toBe(JSON.stringify(backup));
  });
});
