// The account menu and the router both decide whether uptime and the
// leaderboard are open to a reader. They share panelAllowed() so the two can
// never disagree: a menu entry shown to a reader the router sends back home.

import { afterEach, describe, expect, it, vi } from 'vitest';
import { createApp, h, nextTick, ref, type App } from 'vue';
import { createMemoryHistory, createRouter } from 'vue-router';
import AccountMenu from '../src/layouts/AccountMenu.vue';
import { changeLanguage, t } from '../src/composables/useI18n';
import { installPlugins } from '../src/plugins/registry';
import { site, siteInfo } from '../src/stores/session';
import { panelAllowed } from '../src/views/panels';
import { makeAccount } from './fixtures/account';

vi.mock('../src/stores/feedback', () => ({
  feedbackUnread: ref(0),
  refreshFeedbackUnread: vi.fn(async () => {}),
  forgetFeedbackUnread: vi.fn(),
}));

const reader = makeAccount({
  id: 'member', username: 'member', nickname: 'Member', email: '', group_id: 'g', created_at: 0, updated_at: 0,
});

let app: App | undefined;

async function menuText(): Promise<string> {
  const host = document.createElement('div');
  document.body.appendChild(host);
  app = createApp({ render: () => h(AccountMenu, { account: reader }) });
  app.use(createRouter({ history: createMemoryHistory(), routes: [{ path: '/:p(.*)*', component: { render: () => null } }] }));
  app.mount(host);
  await nextTick();
  host.querySelector<HTMLButtonElement>('.oa-account-btn')!.click();
  await vi.waitFor(() => expect(host.querySelector('.oa-acct-head')).not.toBeNull());
  await nextTick();
  return document.body.textContent ?? '';
}

afterEach(() => {
  app?.unmount();
  app = undefined;
  site.value = null;
  installPlugins([]);
});

describe('uptime and leaderboard entries in the account menu', () => {
  it.each([
    ['uptime', 'uptimeTitle', 'health_show_users'],
    ['leaderboard', 'leaderboardTitle', 'leaderboard_show_users'],
  ] as const)('list %s exactly when panelAllowed says the reader may open it', async (panel, titleKey, flag) => {
    await changeLanguage('en');
    for (const published of [false, true]) {
      site.value = { ...siteInfo.value, [flag]: published };
      const listed = (await menuText()).includes(t(titleKey));
      expect(listed).toBe(panelAllowed(panel));
      expect(listed).toBe(published);
      app?.unmount();
      app = undefined;
      document.body.textContent = '';
    }
  });
});
