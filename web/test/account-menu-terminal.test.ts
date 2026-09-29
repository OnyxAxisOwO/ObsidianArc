import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import { createApp, h, nextTick, ref, type App } from 'vue';
import { createMemoryHistory, createRouter } from 'vue-router';
import type { Account } from '../src/api/auth';
import AccountMenu from '../src/layouts/AccountMenu.vue';
import { changeLanguage, t } from '../src/composables/useI18n';

// The menu asks for the unread feedback count when it mounts; nothing here is
// about that, and there is no server to ask.
vi.mock('../src/stores/feedback', () => ({
  feedbackUnread: ref(0),
  refreshFeedbackUnread: vi.fn(async () => {}),
  forgetFeedbackUnread: vi.fn(),
}));

const base: Account = {
  id: 'member', username: 'member', nickname: 'Member', email: '', bio: '', avatar: '',
  role: 'user', status: 'active', group_id: 'g', group_expires_at: 0, group_name: 'Default',
  created_at: 0, updated_at: 0, last_login_at: 0, email_verified: true,
  allow_stats: true, allow_delete_conversations: true,
  api_restricted: false, api_restricted_until: 0, api_restriction_source: '',
};

let app: App | undefined;

async function openMenu(account: Account): Promise<HTMLElement> {
  const host = document.createElement('div');
  document.body.appendChild(host);
  app = createApp({ render: () => h(AccountMenu, { account }) });
  app.use(createRouter({ history: createMemoryHistory(), routes: [{ path: '/:p(.*)*', component: { render: () => null } }] }));
  app.mount(host);
  await nextTick();
  host.querySelector<HTMLButtonElement>('.oa-account-btn')!.click();
  await nextTick();
  return document.body;
}

beforeEach(async () => { await changeLanguage('en'); });
afterEach(() => { app?.unmount(); app = undefined; document.body.textContent = ''; });

// The terminal moved from the backoffice into this menu, for every account
// whose group allows it. Absent is not a refusal: a server too old to send the
// flag is one that did not have the switch, not one that switched it off.
describe('the terminal in the account menu', () => {
  it('is offered when the group allows it, and when the server does not say', async () => {
    expect((await openMenu({ ...base, allow_terminal: true })).textContent).toContain(t('navTerminal'));
    app?.unmount();
    document.body.textContent = '';
    expect((await openMenu({ ...base })).textContent).toContain(t('navTerminal'));
  });

  it('is not offered when the group has it switched off', async () => {
    const body = await openMenu({ ...base, allow_terminal: false });
    expect(body.textContent).toContain(t('apiKeys'));
    expect(body.textContent).not.toContain(t('navTerminal'));
  });
});
