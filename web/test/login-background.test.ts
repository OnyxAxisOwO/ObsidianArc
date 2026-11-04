import { describe, it, expect, vi, beforeEach, afterEach } from 'vitest';
import { createApp, h, nextTick, type App } from 'vue';
import { useLoginBackground } from '../src/composables/useLoginBackground';
import { site } from '../src/stores/session';
import { setThemeMode } from '../src/theme/theme';
import type { SiteInfo } from '../src/api/auth';
import AuthView from '../src/views/AuthView.vue';

vi.mock('vue-router', () => ({
  useRouter: () => ({ push: vi.fn(), replace: vi.fn() }),
  useRoute: () => ({ path: '/login', query: {} }),
}));

describe('useLoginBackground', () => {
  let isPortrait = false;
  // Keyed by query: a change of orientation must not also answer the
  // tablet query, which this mock never matches.
  let listeners: Array<{ query: string; fn: (e?: unknown) => void }> = [];
  const originalMatchMedia = window.matchMedia;

  function setPortrait(val: boolean): void {
    isPortrait = val;
    for (const listener of listeners) {
      if (listener.query.includes('portrait')) listener.fn({ matches: val, media: listener.query });
    }
  }

  beforeEach(() => {
    isPortrait = false;
    listeners = [];
    window.matchMedia = vi.fn((query: string) => ({
      get matches() {
        return query.includes('portrait') ? isPortrait : false;
      },
      media: query,
      onchange: null,
      addListener: vi.fn((fn: (e?: unknown) => void) => { listeners.push({ query, fn }); }),
      removeListener: vi.fn(),
      addEventListener: vi.fn((_type: string, fn: (e?: unknown) => void) => { listeners.push({ query, fn }); }),
      removeEventListener: vi.fn(),
      dispatchEvent: vi.fn(() => false),
    })) as unknown as typeof window.matchMedia;
    setThemeMode('light');
    site.value = null;
  });

  afterEach(() => {
    window.matchMedia = originalMatchMedia;
    setThemeMode('auto');
    site.value = null;
  });

  it('returns empty string when no login background is configured', () => {
    const { loginBgUrl } = useLoginBackground();
    expect(loginBgUrl.value).toBe('');

    site.value = { login_background: {} } as unknown as SiteInfo;
    expect(loginBgUrl.value).toBe('');
  });

  it('resolves correct variant when all 4 variants are present', () => {
    site.value = {
      login_background: {
        landscape_light: '/bg-land-light.jpg',
        landscape_dark: '/bg-land-dark.jpg',
        portrait_light: '/bg-port-light.jpg',
        portrait_dark: '/bg-port-dark.jpg',
      },
    } as unknown as SiteInfo;

    const { loginBgUrl } = useLoginBackground();

    // Landscape · Light
    setPortrait(false);
    setThemeMode('light');
    expect(loginBgUrl.value).toBe('/bg-land-light.jpg');

    // Landscape · Dark
    setThemeMode('dark');
    expect(loginBgUrl.value).toBe('/bg-land-dark.jpg');

    // Portrait · Dark
    setPortrait(true);
    expect(loginBgUrl.value).toBe('/bg-port-dark.jpg');

    // Portrait · Light
    setThemeMode('light');
    expect(loginBgUrl.value).toBe('/bg-port-light.jpg');
  });

  it('falls back from dark mode to light mode if dark variant is absent', () => {
    site.value = {
      login_background: {
        landscape_light: '/bg-land-light.jpg',
        portrait_light: '/bg-port-light.jpg',
      },
    } as unknown as SiteInfo;

    const { loginBgUrl } = useLoginBackground();

    // Landscape dark falls back to landscape light
    setPortrait(false);
    setThemeMode('dark');
    expect(loginBgUrl.value).toBe('/bg-land-light.jpg');

    // Portrait dark falls back to portrait light
    setPortrait(true);
    expect(loginBgUrl.value).toBe('/bg-port-light.jpg');
  });

  it('falls back across orientations if orientation variant is absent', () => {
    // Only landscape variants configured
    site.value = {
      login_background: {
        landscape_light: '/bg-land-light.jpg',
        landscape_dark: '/bg-land-dark.jpg',
      },
    } as unknown as SiteInfo;

    const { loginBgUrl } = useLoginBackground();

    setPortrait(true);
    setThemeMode('light');
    expect(loginBgUrl.value).toBe('/bg-land-light.jpg');

    setThemeMode('dark');
    expect(loginBgUrl.value).toBe('/bg-land-dark.jpg');

    // Only portrait variants configured
    site.value = {
      login_background: {
        portrait_light: '/bg-port-light.jpg',
        portrait_dark: '/bg-port-dark.jpg',
      },
    } as unknown as SiteInfo;

    setPortrait(false);
    setThemeMode('light');
    expect(loginBgUrl.value).toBe('/bg-port-light.jpg');

    setThemeMode('dark');
    expect(loginBgUrl.value).toBe('/bg-port-dark.jpg');
  });

  it('cascades from dark portrait through all fallbacks to landscape light', () => {
    // Only landscape_light exists
    site.value = {
      login_background: {
        landscape_light: '/bg-land-light.jpg',
      },
    } as unknown as SiteInfo;

    const { loginBgUrl } = useLoginBackground();

    // Portrait mode + dark theme -> should cascade to landscape_light
    setPortrait(true);
    setThemeMode('dark');
    expect(loginBgUrl.value).toBe('/bg-land-light.jpg');
  });

  it('falls back from light mode to dark mode if light variant is absent', () => {
    site.value = {
      login_background: {
        landscape_dark: '/bg-land-dark.jpg',
        portrait_dark: '/bg-port-dark.jpg',
      },
    } as unknown as SiteInfo;

    const { loginBgUrl } = useLoginBackground();

    // Landscape light falls back to landscape dark
    setPortrait(false);
    setThemeMode('light');
    expect(loginBgUrl.value).toBe('/bg-land-dark.jpg');

    // Portrait light falls back to portrait dark
    setPortrait(true);
    expect(loginBgUrl.value).toBe('/bg-port-dark.jpg');
  });

  it('cascades from light portrait through all fallbacks to landscape dark', () => {
    // Only landscape_dark exists
    site.value = {
      login_background: {
        landscape_dark: '/bg-land-dark.jpg',
      },
    } as unknown as SiteInfo;

    const { loginBgUrl } = useLoginBackground();

    // Portrait mode + light theme -> should cascade to landscape_dark
    setPortrait(true);
    setThemeMode('light');
    expect(loginBgUrl.value).toBe('/bg-land-dark.jpg');
  });
});

describe('AuthView with login background', () => {
  let app: App | undefined;
  let host: HTMLElement;

  beforeEach(() => {
    host = document.createElement('div');
    document.body.append(host);
  });

  afterEach(() => {
    app?.unmount();
    app = undefined;
    host.remove();
    site.value = null;
  });

  it('renders .has-login-bg and background-image style when login background is set', async () => {
    site.value = {
      name: 'Test Site',
      login_background: {
        landscape_light: '/custom-bg.jpg',
      },
    } as unknown as SiteInfo;

    app = createApp({
      render: () => h(AuthView, { mode: 'login' }),
    });
    app.mount(host);
    await nextTick();

    const authEl = host.querySelector('.oa-auth') as HTMLElement;
    expect(authEl).not.toBeNull();
    expect(authEl.classList.contains('has-login-bg')).toBe(true);
    expect(authEl.style.backgroundImage).toContain('/custom-bg.jpg');
  });

  it('does not render .has-login-bg when login background is absent', async () => {
    site.value = {
      name: 'Test Site',
      login_background: {},
    } as unknown as SiteInfo;

    app = createApp({
      render: () => h(AuthView, { mode: 'login' }),
    });
    app.mount(host);
    await nextTick();

    const authEl = host.querySelector('.oa-auth') as HTMLElement;
    expect(authEl).not.toBeNull();
    expect(authEl.classList.contains('has-login-bg')).toBe(false);
    expect(authEl.style.backgroundImage).toBe('');
  });
});
