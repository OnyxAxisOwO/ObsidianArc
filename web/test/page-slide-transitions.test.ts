import { describe, it, expect, beforeEach, afterEach, vi } from 'vitest';
import { createApp, h, nextTick, ref, type App as VueApp } from 'vue';
import { createMemoryHistory, createRouter, RouterView, useRoute } from 'vue-router';
import App from '../src/App.vue';
import OaPanel from '../src/components/OaPanel.vue';
import { computePageTransition } from '../src/lib/pageTransitions';
import { providePanelHost } from '../src/composables/usePanelHost';

describe('page slide transitions logic (pageTransitions.ts)', () => {
  it('determines smooth horizontal slide direction between chat, terminal, and admin', () => {
    // Opening Admin from Chat: slides left (incoming enters from right)
    expect(computePageTransition('/', '/admin')).toBe('page-slide-left');
    expect(computePageTransition('/settings', '/admin')).toBe('page-slide-left');

    // Leaving Admin back to Chat: slides right (incoming enters from left)
    expect(computePageTransition('/admin', '/')).toBe('page-slide-right');
    expect(computePageTransition('/admin/users', '/')).toBe('page-slide-right');

    // Opening Terminal from Chat: slides left
    expect(computePageTransition('/', '/terminal')).toBe('page-slide-left');

    // Leaving Terminal back to Chat: slides right
    expect(computePageTransition('/terminal', '/')).toBe('page-slide-right');

    // Going from Terminal to Admin: slides left (weight 1 -> 2)
    expect(computePageTransition('/terminal', '/admin')).toBe('page-slide-left');

    // Going from Admin to Terminal: slides right (weight 2 -> 1)
    expect(computePageTransition('/admin', '/terminal')).toBe('page-slide-right');

    // Moving between admin sections does NOT trigger a full-page transition (handled internally)
    expect(computePageTransition('/admin', '/admin/users')).toBeNull();
    expect(computePageTransition('/admin/users', '/admin/settings')).toBeNull();

    // Moving between chat drawers does NOT trigger a full-page transition
    expect(computePageTransition('/', '/settings')).toBeNull();
    expect(computePageTransition('/settings', '/keys')).toBeNull();

    // Auth routes use fade
    expect(computePageTransition('/login', '/')).toBe('page-fade');
    expect(computePageTransition('/', '/login')).toBe('page-fade');
  });
});

describe('App.vue transition wrapper', () => {
  let app: VueApp | null = null;
  let host: HTMLElement;

  beforeEach(() => {
    host = document.createElement('div');
    document.body.appendChild(host);
  });

  afterEach(() => {
    app?.unmount();
    app = null;
    host.remove();
    vi.useRealTimers();
    vi.unstubAllGlobals();
  });

  it('keeps the outgoing route and nested panel intact until the body slide finishes', async () => {
    vi.useFakeTimers();
    vi.stubGlobal('requestAnimationFrame', (callback: FrameRequestCallback) => setTimeout(() => callback(0), 16));
    const root = {
      setup() {
        const route = useRoute();
        return () => h('div', { class: 'test-root' }, [route.path, h(RouterView)]);
      },
    };
    const admin = {
      setup() {
        const route = useRoute();
        return () => h('div', { class: 'test-admin' }, route.path);
      },
    };
    const router = createRouter({
      history: createMemoryHistory(),
      routes: [
        { path: '/', component: root, children: [
          { path: 'settings', component: { render: () => h('span', { class: 'test-panel' }, 'Settings') } },
        ] },
        { path: '/admin/:section?', component: admin },
      ],
    });
    await router.push('/settings');
    app = createApp(App);
    app.use(router);
    await router.isReady();
    app.mount(host);

    const outgoing = host.querySelector('.oa-app-page');
    await router.push('/admin/users');
    await nextTick();
    await vi.advanceTimersByTimeAsync(50);
    expect(outgoing?.isConnected).toBe(true);
    expect(outgoing?.querySelector('.test-root')?.textContent).toBe('/settingsSettings');
    expect(outgoing?.classList.contains('page-slide-left-leave-active')).toBe(true);
    await vi.advanceTimersByTimeAsync(300);
    expect(outgoing?.isConnected).toBe(true);
    await vi.advanceTimersByTimeAsync(100);
    expect(outgoing?.isConnected).toBe(false);
    expect(host.querySelectorAll('.oa-app-page')).toHaveLength(1);

    // A section change updates the mounted page, but leaving the realm must
    // not reset that page to the dashboard during its reverse slide.
    const adminPage = host.querySelector('.oa-app-page');
    await router.push('/admin/models');
    await nextTick();
    expect(host.querySelector('.oa-app-page')).toBe(adminPage);
    expect(adminPage?.textContent).toBe('/admin/models');
    await router.push('/');
    await nextTick();
    await vi.advanceTimersByTimeAsync(50);
    expect(adminPage?.textContent).toBe('/admin/models');
    expect(adminPage?.classList.contains('page-slide-right-leave-active')).toBe(true);
    await vi.advanceTimersByTimeAsync(400);
    expect(adminPage?.isConnected).toBe(false);
    expect(host.querySelectorAll('.oa-app-page')).toHaveLength(1);
  });

  it.each([false, true])('settles a rapid reversal without leftover pages (reduced motion: %s)', async (reduced) => {
    vi.useFakeTimers();
    vi.stubGlobal('requestAnimationFrame', (callback: FrameRequestCallback) => setTimeout(() => callback(0), 16));
    vi.stubGlobal('matchMedia', (query: string) => ({
      matches: reduced && query === '(prefers-reduced-motion: reduce)', media: query,
      addEventListener() {}, removeEventListener() {},
    }));
    const router = createRouter({
      history: createMemoryHistory(),
      routes: [
        { path: '/', component: { render: () => h('div', 'Chat') } },
        { path: '/admin', component: { render: () => h('div', 'Admin') } },
      ],
    });
    app = createApp(App);
    app.use(router);
    await router.isReady();
    app.mount(host);
    await nextTick();
    expect(host.querySelector<HTMLElement>('.oa-app-viewport')?.style.getPropertyValue('--oa-page-duration'))
      .toBe(reduced ? '80ms' : '380ms');

    await router.push('/admin');
    await vi.advanceTimersByTimeAsync(50);
    await router.push('/');
    await vi.advanceTimersByTimeAsync(reduced ? 120 : 420);
    expect(host.querySelectorAll('.oa-app-page')).toHaveLength(1);
    expect(host.querySelector('.oa-app-page')?.textContent).toBe('Chat');
    expect(host.querySelector('.page-slide-left-enter-active, .page-slide-right-leave-active')).toBeNull();
  });

  it('takes its navigation guard back when the page unmounts', async () => {
    const router = createRouter({
      history: createMemoryHistory(),
      routes: [
        { path: '/', component: { render: () => h('div', { class: 'test-chat' }, 'Chat') } },
        { path: '/admin', component: { render: () => h('div', { class: 'test-admin' }, 'Admin') } },
      ],
    });
    // The router is shared across mounts, so what App adds and what it takes
    // back are counted against it directly.
    const addGuard = router.beforeEach.bind(router);
    let registered = 0;
    let removed = 0;
    vi.spyOn(router, 'beforeEach').mockImplementation((guard) => {
      registered += 1;
      const stop = addGuard(guard);
      return () => {
        removed += 1;
        stop();
      };
    });

    app = createApp(App);
    app.use(router);
    await router.isReady();
    app.mount(host);
    await nextTick();
    expect(registered - removed).toBe(1);

    app.unmount();
    app = null;
    expect(registered - removed).toBe(0);
  });
});

describe('OaPanel and TerminalPanel immediateClose', () => {
  let app: VueApp | null = null;
  let host: HTMLElement;

  beforeEach(() => {
    host = document.createElement('div');
    document.body.appendChild(host);
  });

  afterEach(() => {
    app?.unmount();
    app = null;
    host.remove();
  });

  it('OaPanel emits close immediately when immediateClose is true', async () => {
    let closed = false;
    app = createApp({
      setup() {
        const row = document.createElement('div');
        host.appendChild(row);
        providePanelHost(ref(row));
        return () => h(OaPanel, {
          title: 'Immediate Test',
          immediateClose: true,
          onClose: () => { closed = true; },
        });
      },
    });
    app.mount(host);
    await nextTick();

    const closeBtn = host.querySelector<HTMLButtonElement>('.oa-icon-btn');
    expect(closeBtn).not.toBeNull();
    closeBtn?.click();

    // With immediateClose, onClose is called synchronously without waiting 340ms ZOOM_MS
    expect(closed).toBe(true);
  });
});

describe('chat history rail on the terminal route', () => {
  it('ChatSurface conditionally omits ChatSidebar when route is /terminal', async () => {
    const { default: ChatSurface } = await import('../src/chat/ChatSurface.vue');

    const router = createRouter({
      history: createMemoryHistory(),
      routes: [
        { path: '/', component: ChatSurface },
        { path: '/terminal', component: ChatSurface },
      ],
    });

    const hostEl = document.createElement('div');
    document.body.appendChild(hostEl);

    await router.push('/terminal');
    await router.isReady();

    const appInstance = createApp({
      setup() {
        const dummyHost = ref(document.createElement('div'));
        providePanelHost(dummyHost);
        return () => h(ChatSurface);
      },
    });
    appInstance.use(router);
    appInstance.mount(hostEl);
    await nextTick();

    // On /terminal, ChatSidebar must not be rendered
    expect(hostEl.querySelector('.ai-chat-sidebar')).toBeNull();

    // Navigating back to / mounts ChatSidebar
    await router.push('/');
    await nextTick();
    expect(hostEl.querySelector('.ai-chat-sidebar')).not.toBeNull();

    appInstance.unmount();
    hostEl.remove();
  });
});
