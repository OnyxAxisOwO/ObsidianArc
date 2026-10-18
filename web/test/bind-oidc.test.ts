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
import { api, ApiError } from '@/api/client';
import { changeLanguage, t } from '@/composables/useI18n';
import { forget, site, siteInfo } from '@/stores/session';
import BindOIDCView from '@/views/BindOIDCView.vue';
import { settle, stubLocation } from './helpers';
import { makeAccount } from './fixtures/account';

const SSO = { id: 'oidc', name: 'Campus SSO' };

const ACCOUNT = makeAccount({
  id: 'me', username: 'member', nickname: 'Member', email: '', group_id: 'g', created_at: 0, updated_at: 0,
});

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

  it('asks for the password in the sign-in form shell, so the field is drawn at its size', async () => {
    vi.spyOn(api, 'post').mockRejectedValue(new ApiError(403, 'password_required', 'password required'));
    await mountAt('/bind-oidc');

    host.querySelector<HTMLButtonElement>('.oa-btn-block')!.click();
    await settle();

    // _app.scss sizes .oa-auth-form .oa-field input; outside the shell the
    // password stood smaller than every other sign-in field, and the field, the
    // error and the button had no gap between them.
    const form = host.querySelector('.oa-auth-form');
    expect(form?.querySelector('.oa-field input[type="password"]')).not.toBeNull();
    expect(form?.querySelector('.oa-btn-block')).not.toBeNull();
  });
});
