// "Switch account" on the backoffice refusal signs the reader out first. When
// that sign-out fails — the session is already gone, or the network is down —
// the reader must still be sent to the sign-in page, and the failure must not
// escape as an unhandled rejection.

import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import { createApp, h, type App } from 'vue';
import { createMemoryHistory, createRouter } from 'vue-router';
import * as authApi from '../src/api/auth';
import { ApiError } from '../src/api/client';
import { changeLanguage, t } from '../src/composables/useI18n';
import UnauthorizedModal from '../src/views/UnauthorizedModal.vue';
import { settle } from './helpers';

let app: App | undefined;
let host: HTMLElement;

beforeEach(async () => {
  await changeLanguage('en');
  host = document.createElement('div');
  document.body.append(host);
});

afterEach(() => {
  app?.unmount();
  app = undefined;
  document.body.textContent = '';
  vi.restoreAllMocks();
});


async function mountModal() {
  const router = createRouter({
    history: createMemoryHistory(),
    routes: [{ path: '/:pathMatch(.*)*', component: { render: () => null } }],
  });
  await router.push('/admin');
  await router.isReady();
  const replace = vi.spyOn(router, 'replace');
  app = createApp({ render: () => h(UnauthorizedModal) });
  app.use(router);
  app.mount(host);
  await settle();
  return { replace };
}

function switchButton(): HTMLButtonElement {
  const found = [...document.body.querySelectorAll<HTMLButtonElement>('button')]
    .find((node) => node.textContent?.trim() === t('switchAccount'));
  if (!found) throw new Error('Missing switch-account button');
  return found;
}

describe('switching account from the backoffice refusal', () => {
  it('still leaves for the sign-in page when the sign-out request fails', async () => {
    vi.spyOn(authApi, 'logout').mockRejectedValue(new ApiError(401, 'unauthenticated', 'session gone'));
    const { replace } = await mountModal();

    switchButton().click();
    await settle();

    expect(replace).toHaveBeenCalledWith('/login');
  });

  it('still leaves for the sign-in page when the network is down', async () => {
    vi.spyOn(authApi, 'logout').mockRejectedValue(new TypeError('Failed to fetch'));
    const { replace } = await mountModal();

    switchButton().click();
    await settle();

    expect(replace).toHaveBeenCalledWith('/login');
  });
});
