import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import { createApp, h, type App } from 'vue';
import { createMemoryHistory, createRouter, RouterView, type Router } from 'vue-router';
import AuthView from '../src/views/AuthView.vue';
import { changeLanguage, t } from '../src/composables/useI18n';
import { forget, site, siteInfo } from '../src/stores/session';
import { settle, button } from './helpers';

const originalTransition = document.startViewTransition;
const originalMedia = window.matchMedia;
let app: App;
let host: HTMLElement;
let router: Router;
let reduced = false;
let skipped = false;

const transition = vi.fn((update: Parameters<Document['startViewTransition']>[0]) => {
  if (typeof update !== 'function') throw new Error('Expected a DOM update callback');
  const done = Promise.resolve().then(update);
  return {
    ready: skipped ? Promise.reject(new DOMException('Snapshot superseded', 'AbortError')) : Promise.resolve(),
    finished: done,
    updateCallbackDone: done,
  } as ViewTransition;
});

beforeEach(async () => {
  await changeLanguage('en');
  forget();
  reduced = false;
  skipped = false;
  transition.mockClear();
  document.startViewTransition = transition;
  window.matchMedia = vi.fn((query: string) => ({
    matches: query.includes('prefers-reduced-motion') && reduced,
    media: query,
    onchange: null,
    addListener() {},
    removeListener() {},
    addEventListener() {},
    removeEventListener() {},
    dispatchEvent: () => true,
  }));
  site.value = { ...siteInfo.value, registration_enabled: true };
  host = document.createElement('div');
  document.body.append(host);
  router = createRouter({
    history: createMemoryHistory(),
    routes: [
      { path: '/login', component: AuthView, props: { mode: 'login' } },
      { path: '/register', component: AuthView, props: { mode: 'register' } },
    ],
  });
  app = createApp({ render: () => h(RouterView) });
  app.use(router);
  await router.push('/login?next=%2Fkeys&invite=JOIN-CODE');
  await router.isReady();
  app.mount(host);
  await settle();
});

afterEach(() => {
  app.unmount();
  host.remove();
  site.value = null;
  forget();
  if (originalTransition) document.startViewTransition = originalTransition;
  else Reflect.deleteProperty(document, 'startViewTransition');
  window.matchMedia = originalMedia;
  vi.restoreAllMocks();
});

describe('sign-in and registration expansion', () => {
  it.each(['center', 'left', 'right', 'left-panel', 'right-panel'] as const)(
    'animates both directions in the %s layout without replacing the form or losing typed values',
    async (position) => {
      site.value = { ...siteInfo.value, auth_card_position: position };
      const form = host.querySelector('form');
      const identity = host.querySelector<HTMLInputElement>('input[autocomplete="username"]')!;
      const password = host.querySelector<HTMLInputElement>('input[type="password"]')!;
      for (const [input, value] of [[identity, 'alice'], [password, 'a-good-password']] as const) {
        input.value = value;
        input.dispatchEvent(new Event('input', { bubbles: true }));
      }
      button(host, t('createOne')).click();
      await settle();
      expect(router.currentRoute.value.path).toBe('/register');
      expect(host.querySelector('input[autocomplete="email"]')).not.toBeNull();
      expect(host.querySelector('form')).toBe(form);
      expect(host.querySelector('input[autocomplete="username"]')).toBe(identity);
      expect(document.activeElement).toBe(identity);
      expect(password.value).toBe('a-good-password');
      button(host, t('signIn')).click();
      await settle();
      expect(router.currentRoute.value.path).toBe('/login');
      expect(host.querySelector('input[autocomplete="email"]')).toBeNull();
      expect(host.querySelector('form')).toBe(form);
      expect(identity.value).toBe('alice');
      expect(document.activeElement).toBe(identity);
      expect(password.value).toBe('a-good-password');
      expect(router.currentRoute.value.query).toEqual({ next: '/keys', invite: 'JOIN-CODE' });
      expect(transition).toHaveBeenCalledTimes(2);
    },
  );

  it('keeps navigation working when a snapshot is skipped', async () => {
    skipped = true;
    button(host, t('createOne')).click();
    await settle();
    expect(router.currentRoute.value.path).toBe('/register');
    expect(host.querySelector('input[autocomplete="email"]')).not.toBeNull();
  });

  it('switches directly when reduced motion is requested', async () => {
    reduced = true;
    button(host, t('createOne')).click();
    await settle();
    expect(transition).not.toHaveBeenCalled();
    expect(router.currentRoute.value.path).toBe('/register');
  });

  it('switches directly when the browser has no view-transition API', async () => {
    Reflect.deleteProperty(document, 'startViewTransition');
    button(host, t('createOne')).click();
    await settle();
    expect(transition).not.toHaveBeenCalled();
    expect(router.currentRoute.value.path).toBe('/register');
  });
});
