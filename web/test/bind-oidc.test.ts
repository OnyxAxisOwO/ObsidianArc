// The OpenID Connect binding screen as a reader meets it after the connect flow
// comes back.
//
// The callback's redirect carries the destination the reader asked for, and the
// screen has to remember it before it strips the query string: the strip is not
// awaited, so by the time the link is confirmed the query no longer says where
// to go. A failed link says why in words, not with the code it arrived with.
//
// These use a real router rather than the stub the sign-in suite uses, because
// the stub's query never changes and cannot show the strip happening.

import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import { createApp, h, type App } from 'vue';
import { createMemoryHistory, createRouter, type Router } from 'vue-router';
import type { Account } from '@/api/auth';
import { api } from '@/api/client';
import { changeLanguage, t } from '@/composables/useI18n';
import { forget, site, siteInfo } from '@/stores/session';
import BindOIDCView from '@/views/BindOIDCView.vue';
import { settle, stubLocation } from './helpers';

const SSO = { id: 'oidc', name: 'Campus SSO' };

const ACCOUNT: Account = {
  id: 'me', username: 'member', nickname: 'Member', email: '', bio: '', avatar: '',
  role: 'user', status: 'active', group_id: 'g', group_expires_at: 0, group_name: 'Default',
  created_at: 0, updated_at: 0, last_login_at: 0, email_verified: true,
  allow_stats: true, allow_delete_conversations: true,
  api_restricted: false, api_restricted_until: 0, api_restriction_source: '',
};

let app: App | null = null;
let host: HTMLElement;

beforeEach(async () => {
  await changeLanguage('en');
  host = document.createElement('div');
  document.body.append(host);
  site.value = { ...siteInfo.value, oauth: [SSO] };
});

afterEach(() => {
  app?.unmount();
  app = null;
  site.value = null;
  forget();
  document.body.textContent = '';
  vi.restoreAllMocks();
});

// Stands in for a network round trip, so the strip of the query has finished
// before the account is re-read, as it does against a real server.
function delay(): Promise<void> {
  return new Promise((resolve) => setTimeout(resolve, 10));
}

async function mountAt(url: string): Promise<Router> {
  const router = createRouter({
    history: createMemoryHistory(),
    routes: [{ path: '/:pathMatch(.*)*', component: { render: () => null } }],
  });
  await router.push(url);
  await router.isReady();
  app = createApp({ render: () => h(BindOIDCView) });
  app.use(router);
  app.mount(host);
  await settle();
  return router;
}

describe('the bind screen after the connect flow comes back', () => {
  it('sends the reader on to the page they asked for, not to the chat', async () => {
    const destination = '/settings?tab=security';
    vi.spyOn(api, 'get').mockImplementation(async (url) => {
      await delay();
      if (url !== '/api/auth/me') throw new Error(`unexpected request: ${url}`);
      return { user: ACCOUNT, preferences: {} };
    });

    const router = await mountAt(`/bind-oidc?next=${encodeURIComponent(destination)}&oauth=connected`);
    await delay();
    await settle();

    expect(router.currentRoute.value.fullPath).toBe(destination);
  });

  it('hands a server page to a full navigation, since the router has no screen for it', async () => {
    const destination = '/oauth/authorize?client_id=other&state=abc';
    vi.spyOn(api, 'get').mockImplementation(async () => {
      await delay();
      return { user: ACCOUNT, preferences: {} };
    });
    const { assign, restore } = stubLocation();
    try {
      await mountAt(`/bind-oidc?next=${encodeURIComponent(destination)}&oauth=connected`);
      await delay();
      await settle();

      expect(assign).toHaveBeenCalledWith(destination);
    } finally {
      restore();
    }
  });

  it('says why the link failed, in words, and takes the code out of the address bar', async () => {
    const router = await mountAt('/bind-oidc?oauth_error=already_linked');

    expect(host.querySelector('.oa-auth-error')?.textContent).toBe(t('oauthAlreadyLinked'));
    expect(router.currentRoute.value.query).toEqual({});
  });

  it('says the general failure for a code it does not know, rather than the code itself', async () => {
    await mountAt('/bind-oidc?oauth_error=made_up_code');

    expect(host.querySelector('.oa-auth-error')?.textContent).toBe(t('oauthFailed'));
    expect(host.textContent).not.toContain('made_up_code');
  });
});
