import { afterEach, beforeEach, expect, it, vi } from 'vitest';
import { createApp, nextTick, type App } from 'vue';
import AppearanceSection from '../src/views/settings/AppearanceSection.vue';
import { asciiFieldWanted } from '../src/chat/useChat';
import { t } from '../src/composables/useI18n';
import * as auth from '../src/api/auth';
import { adopt, forget, site, siteInfo } from '../src/stores/session';

// The instance's switch is a ceiling: off there, nobody sees the field and
// nobody is offered a switch that would do nothing. On there, each reader's
// own switch decides, and a reader who never touched it gets the field.

const account: auth.Account = {
  id: 'member', username: 'member', email: '', nickname: '', avatar: '', bio: '', role: 'user',
  group_id: '', group_expires_at: 0, group_name: '', status: 'active', created_at: 0, updated_at: 0,
  last_login_at: 0, email_verified: true, allow_stats: true, allow_delete_conversations: true,
  api_restricted: false, api_restricted_until: 0, api_restriction_source: '',
};

let app: App | undefined;
beforeEach(() => {
  vi.spyOn(auth, 'savePreferences').mockResolvedValue({ preferences: {} });
});
afterEach(() => {
  app?.unmount(); app = undefined; document.body.textContent = '';
  site.value = null; forget(); vi.restoreAllMocks();
});

function mountAppearance(): HTMLElement {
  const host = document.createElement('div');
  document.body.appendChild(host);
  app = createApp(AppearanceSection);
  app.mount(host);
  return host;
}

function fieldSwitch(host: HTMLElement): HTMLInputElement | null {
  const row = [...host.querySelectorAll('label, .oa-field, .oa-switch-field')]
    .find((node) => node.textContent?.includes(t('asciiField')));
  return row?.querySelector('input') ?? null;
}

it('draws the field for a reader who never chose', () => {
  adopt(account);
  expect(asciiFieldWanted.value).toBe(true);
});

it('lets a reader turn it off for themselves, and saves that', async () => {
  adopt(account);
  const host = mountAppearance();
  const input = fieldSwitch(host);
  expect(input).not.toBeNull();
  input!.click();
  await nextTick();
  expect(auth.savePreferences).toHaveBeenLastCalledWith({ ascii_field: false });
  expect(asciiFieldWanted.value).toBe(false);
});

it('is off for everyone, with no switch offered, when the instance turns it off', () => {
  site.value = { ...siteInfo.value, ascii_field: false };
  adopt(account, { ascii_field: true });
  expect(asciiFieldWanted.value).toBe(false);
  expect(fieldSwitch(mountAppearance())).toBeNull();
});
