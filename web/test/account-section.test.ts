// The account panel's three commitments that go wrong quietly: which password
// form an account is shown (it is not known until the connections load), what
// a half-finished import says about what it already wrote, and that an export
// is saved in the form the importer will accept.

import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import { createApp, h, nextTick, ref, shallowRef, type App, type Component } from 'vue';
import * as authApi from '../src/api/auth';
import * as backupApi from '../src/api/backup';
import { ApiError } from '../src/api/client';
import * as consentApi from '../src/api/consent';
import * as oauthApi from '../src/api/oauth';
import { providePanelHost } from '../src/composables/usePanelHost';
import { changeLanguage, t } from '../src/composables/useI18n';
import { adopt, forget } from '../src/stores/session';
import AccountSection from '../src/views/settings/AccountSection.vue';
import { settle, button } from './helpers';
import { makeAccount } from './fixtures/account';

const route = { path: '/settings', fullPath: '/settings', query: {} as Record<string, string>, hash: '', meta: {} };
const router = { replace: vi.fn() };
vi.mock('vue-router', () => ({
  useRoute: () => route,
  useRouter: () => router,
}));

const ACCOUNT = makeAccount({
  id: 'member', username: 'member', email: 'member@example.test', nickname: '', group_id: '', group_name: '',
  created_at: 0, updated_at: 0,
});

let app: App | undefined;
let host: HTMLElement;

beforeEach(async () => {
  await changeLanguage('en');
  route.query = {};
  router.replace.mockClear();
  adopt(ACCOUNT);
  vi.spyOn(consentApi, 'fetchAuthorizations').mockResolvedValue({ authorizations: [] });
  host = document.createElement('div');
  document.body.append(host);
});

afterEach(() => {
  app?.unmount();
  app = undefined;
  vi.restoreAllMocks();
  forget();
});

async function mount(component: Component): Promise<void> {
  const panels = document.createElement('div');
  document.body.append(panels);
  app = createApp({
    setup() {
      providePanelHost(shallowRef(panels));
      return () => h(component, { query: '', active: true });
    },
  });
  app.mount(host);
  await settle();
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
    expect(button(host, t('retry'))).toBeTruthy();
  });

  it('shows the form the account actually has once a retry succeeds', async () => {
    const fetchConnections = vi.spyOn(oauthApi, 'fetchConnections')
      .mockRejectedValueOnce(new ApiError(500, 'internal', 'Could not reach the server.'))
      .mockResolvedValueOnce({ connections: [], providers: [], has_password: false });
    await mount(AccountSection);
    button(host, t('retry')).click();
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
    vi.spyOn(oauthApi, 'fetchConnections').mockResolvedValue({ connections: [], providers: [], has_password: false });
    await mount(AccountSection);

    type(emailInput(), 'moved@example.test');
    await settle();

    expect(host.textContent).not.toContain(t('emailChangePasswordHint'));
  });

  it('says a wrong current password in the page language, not the server sentence', async () => {
    vi.spyOn(oauthApi, 'fetchConnections').mockResolvedValue({ connections: [], providers: [], has_password: true });
    vi.spyOn(authApi, 'changePassword').mockRejectedValue(
      new ApiError(400, 'current_password_wrong', 'Current password is incorrect.'),
    );
    await mount(AccountSection);

    type(passwordInputs()[0]!, 'not-the-password');
    type(passwordInputs()[1]!, 'a-better-password');
    button(host, t('changePassword')).click();
    await settle();

    expect(host.textContent).toContain(t('currentPasswordWrong'));
    expect(host.textContent).not.toContain('Current password is incorrect.');
  });
  // The server answers these with a code; without a sentence of the page's own
  // the reader saw the server's English on a translated screen.
  it.each([
    ['password_unchanged', 'The new password is the same as the current one.', 'errPasswordUnchanged'],
    ['password_too_short', 'Password must be at least 8 characters.', 'passwordTooShort'],
    ['password_too_long', 'Password must be at most 256 characters.', 'errPasswordTooLong'],
  ] as const)('says %s in the page language', async (code, sentence, key) => {
    await changeLanguage('zh');
    vi.spyOn(oauthApi, 'fetchConnections').mockResolvedValue({ connections: [], providers: [], has_password: true });
    vi.spyOn(authApi, 'changePassword').mockRejectedValue(new ApiError(400, code, sentence));
    await mount(AccountSection);

    type(passwordInputs()[0]!, 'the-password');
    type(passwordInputs()[1]!, 'the-password');
    button(host, t('changePassword')).click();
    await settle();

    expect(host.textContent).toContain(t(key));
    expect(host.textContent).not.toContain(sentence);
  });
});

describe('the profile section', () => {
  it.each([
    ['email_required', 'An email address is required on this server.', 'errEmailRequired'],
    ['invalid_email', 'Email address is not valid.', 'emailInvalid'],
    ['bio_too_long', 'Bio must be 500 characters or fewer.', 'errBioTooLong'],
    ['avatar_too_long', 'Avatar is too large.', 'imageTooLarge'],
  ] as const)('says %s in the page language', async (code, sentence, key) => {
    await changeLanguage('zh');
    vi.spyOn(oauthApi, 'fetchConnections').mockResolvedValue({ connections: [], providers: [], has_password: true });
    vi.spyOn(authApi, 'updateProfile').mockRejectedValue(new ApiError(400, code, sentence));
    await mount(AccountSection);

    button(host, t('save')).click();
    await settle();

    expect(host.textContent).toContain(t(key));
    expect(host.textContent).not.toContain(sentence);
  });
});

describe('the lists the section reads', () => {
  async function mountShown(shown: { value: boolean }): Promise<void> {
    app = createApp({
      setup() {
        providePanelHost(shallowRef(document.createElement('div')));
        return () => h(AccountSection, { query: '', active: shown.value });
      },
    });
    app.mount(host);
    await settle();
  }

  it('waits until the section is first shown, and reads them once', async () => {
    const connections = vi.spyOn(oauthApi, 'fetchConnections')
      .mockResolvedValue({ connections: [], providers: [], has_password: true });
    const authorizations = vi.mocked(consentApi.fetchAuthorizations);
    const shown = ref(false);
    await mountShown(shown);
    expect(connections).not.toHaveBeenCalled();
    expect(authorizations).not.toHaveBeenCalled();

    shown.value = true;
    await settle();
    expect(connections).toHaveBeenCalledTimes(1);
    expect(authorizations).toHaveBeenCalledTimes(1);

    shown.value = false;
    await nextTick();
    shown.value = true;
    await settle();
    expect(connections).toHaveBeenCalledTimes(1);
    expect(authorizations).toHaveBeenCalledTimes(1);
  });

  it('still says a failed read once the section is shown', async () => {
    vi.spyOn(oauthApi, 'fetchConnections').mockRejectedValue(new ApiError(500, 'internal', 'Could not reach the server.'));
    const shown = ref(false);
    await mountShown(shown);
    expect(host.textContent).not.toContain(t('failed'));

    shown.value = true;
    await settle();
    expect(host.textContent).toContain(t('failed'));
    expect(button(host, t('retry'))).toBeTruthy();
  });

  it('takes a connection outcome from the address while it is still hidden', async () => {
    route.query = { oauth: 'connected' };
    const connections = vi.spyOn(oauthApi, 'fetchConnections')
      .mockResolvedValue({ connections: [], providers: [], has_password: true });
    await mountShown(ref(false));

    expect(router.replace).toHaveBeenCalledTimes(1);
    expect(host.textContent).toContain(t('oauthConnected'));
    expect(connections).not.toHaveBeenCalled();
  });
});

describe('the data section', () => {
  it('says how much a full account let in, rather than a generic failure', async () => {
    vi.spyOn(oauthApi, 'fetchConnections').mockResolvedValue({ connections: [], providers: [], has_password: true });
    vi.spyOn(backupApi, 'pickJSONFile').mockResolvedValue({ obsidian_arc_export: 1, conversations: [] });
    vi.spyOn(backupApi, 'importAccount').mockRejectedValue(new ApiError(409, 'storage_full', 'This account is full.', {
      conversations: 3, messages: 41, skipped: 0, preferences: false,
    }));
    await mount(AccountSection);

    button(host, t('importData')).click();
    await settle();

    expect(host.textContent).toContain(t('importStorageFull', { conversations: 3, messages: 41 }));
    expect(host.textContent).not.toContain('This account is full.');
  });

  it('says a full account is full, not that an import stopped part way, when the import wrote nothing', async () => {
    vi.spyOn(oauthApi, 'fetchConnections').mockResolvedValue({ connections: [], providers: [], has_password: true });
    vi.spyOn(backupApi, 'pickJSONFile').mockResolvedValue({ obsidian_arc_export: 1, conversations: [] });
    vi.spyOn(backupApi, 'importAccount').mockRejectedValue(new ApiError(409, 'storage_full', 'This account is full.', {
      conversations: 0, messages: 0, skipped: 0, preferences: false,
    }));
    await mount(AccountSection);

    button(host, t('importData')).click();
    await settle();

    expect(host.textContent).toContain(t('errStorageFull'));
    expect(host.textContent).not.toContain(t('importStorageFull', { conversations: 0, messages: 0 }));
  });

  it('says what a failure part way through an import wrote, whatever its code', async () => {
    vi.spyOn(oauthApi, 'fetchConnections').mockResolvedValue({ connections: [], providers: [], has_password: true });
    vi.spyOn(backupApi, 'pickJSONFile').mockResolvedValue({ obsidian_arc_export: 1, conversations: [] });
    vi.spyOn(backupApi, 'importAccount').mockRejectedValue(new ApiError(500, 'internal', 'Something went wrong on our side.', {
      conversations: 2, messages: 9, skipped: 0, preferences: false,
    }));
    await mount(AccountSection);

    button(host, t('importData')).click();
    await settle();

    expect(host.textContent).toContain(t('importPartialFailed', { conversations: 2, messages: 9 }));
    expect(host.textContent).not.toContain('Something went wrong on our side.');
  });

  it('saves an export compact, the form the importer accepts', async () => {
    vi.spyOn(oauthApi, 'fetchConnections').mockResolvedValue({ connections: [], providers: [], has_password: true });
    const backup: backupApi.BackupDocument = { obsidian_arc_export: 1, exported_at: 0, conversations: [] };
    vi.spyOn(backupApi, 'exportAccount').mockResolvedValue(backup);
    const save = vi.spyOn(backupApi, 'saveAsFile').mockImplementation(() => undefined);
    await mount(AccountSection);

    button(host, t('exportData')).click();
    await settle();

    expect(save).toHaveBeenCalledTimes(1);
    expect(save.mock.calls[0]![1]).toBe(JSON.stringify(backup));
  });
});
