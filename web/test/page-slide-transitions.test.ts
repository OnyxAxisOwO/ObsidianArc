import { describe, it, expect, beforeEach, afterEach, vi } from 'vitest';
import { createApp, h, nextTick, ref, type App as VueApp } from 'vue';
import { createMemoryHistory, createRouter } from 'vue-router';
import App from '../src/App.vue';
import OaPanel from '../src/components/OaPanel.vue';
import {
  computePageTransition,
  getPageKey,
  getRouteWeight,
} from '../src/lib/pageTransitions';
import { providePanelHost } from '../src/composables/usePanelHost';

describe('page slide transitions logic (pageTransitions.ts)', () => {
  it('correctly categorizes route keys so sub-routes share keys while pages differ', () => {
    expect(getPageKey('/')).toBe('root');
    expect(getPageKey('/settings')).toBe('root');
    expect(getPageKey('/keys')).toBe('root');
    expect(getPageKey('/usage')).toBe('root');

    expect(getPageKey('/terminal')).toBe('terminal');

    expect(getPageKey('/admin')).toBe('admin');
    expect(getPageKey('/admin/users')).toBe('admin');
    expect(getPageKey('/admin/models')).toBe('admin');
    expect(getPageKey('/admin/settings')).toBe('admin');

    expect(getPageKey('/login')).toBe('auth');
    expect(getPageKey('/register')).toBe('auth');
    expect(getPageKey('/two-factor')).toBe('auth');
    expect(getPageKey('/verify')).toBe('auth');
    expect(getPageKey('/oauth/consent')).toBe('auth');
    expect(getPageKey('/oauth/complete')).toBe('auth');
  });

  it('assigns correct weights: chat < terminal < admin', () => {
    expect(getRouteWeight('/')).toBe(0);
    expect(getRouteWeight('/settings')).toBe(0);
    expect(getRouteWeight('/terminal')).toBe(1);
    expect(getRouteWeight('/admin')).toBe(2);
    expect(getRouteWeight('/admin/users')).toBe(2);
  });

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
  });

  it('renders .oa-app-viewport and .oa-app-page containers', async () => {
    const router = createRouter({
      history: createMemoryHistory(),
      routes: [
        { path: '/', component: { render: () => h('div', { class: 'test-chat' }, 'Chat') } },
        { path: '/admin', component: { render: () => h('div', { class: 'test-admin' }, 'Admin') } },
      ],
    });

    app = createApp(App);
    app.use(router);
    await router.isReady();
    app.mount(host);
    await nextTick();

    const viewport = host.querySelector('.oa-app-viewport');
    expect(viewport).not.toBeNull();

    const page = host.querySelector('.oa-app-page');
    expect(page).not.toBeNull();
    expect(page?.textContent).toContain('Chat');
  });

  it('transitions page container when navigating from / to /admin and back', async () => {
    const router = createRouter({
      history: createMemoryHistory(),
      routes: [
        { path: '/', component: { render: () => h('div', { class: 'test-chat' }, 'Chat') } },
        { path: '/admin', component: { render: () => h('div', { class: 'test-admin' }, 'Admin') } },
      ],
    });

    app = createApp(App);
    app.use(router);
    await router.isReady();
    app.mount(host);
    await nextTick();

    expect(host.textContent).toContain('Chat');

    // Navigate to admin
    await router.push('/admin');
    await nextTick();

    expect(host.textContent).toContain('Admin');

    // Navigate back to chat
    await router.push('/');
    await nextTick();

    expect(host.textContent).toContain('Chat');
  });

  it('navigates seamlessly across Chat, Terminal, and Admin in sequence', async () => {
    const router = createRouter({
      history: createMemoryHistory(),
      routes: [
        { path: '/', component: { render: () => h('div', { class: 'page-chat' }, 'Chat') } },
        { path: '/terminal', component: { render: () => h('div', { class: 'page-terminal' }, 'Terminal') } },
        { path: '/admin', component: { render: () => h('div', { class: 'page-admin' }, 'Admin') } },
      ],
    });

    app = createApp(App);
    app.use(router);
    await router.isReady();
    app.mount(host);
    await nextTick();

    expect(host.textContent).toContain('Chat');

    // Chat -> Terminal
    await router.push('/terminal');
    await nextTick();
    expect(host.textContent).toContain('Terminal');

    // Terminal -> Admin
    await router.push('/admin');
    await nextTick();
    expect(host.textContent).toContain('Admin');

    // Admin -> Terminal
    await router.push('/terminal');
    await nextTick();
    expect(host.textContent).toContain('Terminal');

    // Terminal -> Chat
    await router.push('/');
    await nextTick();
    expect(host.textContent).toContain('Chat');
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

describe('page slide stylesheet specifications', () => {
  it('contains composite-friendly transitions, pseudo-element depth shadow, and reduced-motion styles', async () => {
    const fs = await import('node:fs');
    const path = await import('node:path');
    const appScssPath = path.resolve(__dirname, '../src/styles/_app.scss');
    const appScss = fs.readFileSync(appScssPath, 'utf8');

    // Smooth non-linear cubic-bezier easing, through the shared token so the
    // slide moves on the same curve as every other arrival.
    expect(appScss).toContain('transition: transform $oa-slide-duration var(--ai-ease-out)');
    const chatScss = fs.readFileSync(path.resolve(__dirname, '../src/styles/_chat.scss'), 'utf8');
    expect(chatScss).toContain('--ai-ease-out: cubic-bezier(0.16, 1, 0.3, 1);');

    // Hardware accelerated 3D transforms
    expect(appScss).toContain('translate3d(100%, 0, 0)');
    expect(appScss).toContain('translate3d(-26%, 0, 0)');
    expect(appScss).toContain('transform-style: preserve-3d');
    expect(appScss).toContain('backface-visibility: hidden');

    // Pseudo-element shadow overlay to avoid costly per-frame box-shadow rasterization
    expect(appScss).toContain('.page-slide-left-enter-active::before');
    expect(appScss).toContain('.page-slide-right-leave-active::before');

    // Conflicting vertical oaRise animation suppressed during full-page horizontal slide
    expect(appScss).toContain('.oa-admin-main.enter-rise');
    expect(appScss).toContain('animation: none !important');

    // Reduced motion media query provides clean cross-fade without layout stacking
    expect(appScss).toContain('@media (prefers-reduced-motion: reduce)');
    expect(appScss).toContain('position: absolute');
    expect(appScss).toContain('transform: none !important');
  });

  it('does not clip the depth shadow that sits outside a sliding layer', async () => {
    const fs = await import('node:fs');
    const path = await import('node:path');
    const appScss = fs.readFileSync(path.resolve(__dirname, '../src/styles/_app.scss'), 'utf8');

    // An overflow clip on the layer would cut off the ::before shadow at left: -28px.
    const layer = /\.page-slide-left-enter-active,\s*\.page-slide-left-leave-active,\s*\.page-slide-right-enter-active,\s*\.page-slide-right-leave-active \{([^}]*)\}/
      .exec(appScss)?.[1] ?? '';
    expect(layer).toMatch(/will-change:\s*transform, opacity/);
    expect(layer).not.toMatch(/overflow\s*:\s*hidden/);
  });

  it('constrains horizontal slide transition to inner workspace container (.oa-chat-root) while pinning .oa-header', async () => {
    const fs = await import('node:fs');
    const path = await import('node:path');
    const surfacesScssPath = path.resolve(__dirname, '../src/styles/_surfaces.scss');
    const surfacesScss = fs.readFileSync(surfacesScssPath, 'utf8');

    // Top header is static / pinned during page slide transitions with smooth cross-fade
    expect(surfacesScss).toContain('.oa-header');
    expect(surfacesScss).toContain('transform: none !important');
    expect(surfacesScss).toContain('animation: none !important');
    expect(surfacesScss).toContain('.page-slide-left-enter-active .oa-header');
    expect(surfacesScss).toContain('transition: opacity 0.24s ease !important');
    expect(surfacesScss).toContain('.page-slide-left-leave-active .oa-header');
    expect(surfacesScss).toContain('transition: opacity 0.18s ease !important');

    // Inner workspace content container slides with hardware acceleration
    expect(surfacesScss).toContain('.oa-workspace > .oa-chat-root');
    expect(surfacesScss).toContain('translate3d(100%, 0, 0)');
    expect(surfacesScss).toContain('translate3d(-26%, 0, 0)');
    expect(surfacesScss).toContain('transform-style: preserve-3d');
    expect(surfacesScss).toContain('backface-visibility: hidden');

    // Pseudo-element shadow attached to inner container
    expect(surfacesScss).toContain('.page-slide-left-enter-active .oa-workspace > .oa-chat-root::before');
    expect(surfacesScss).toContain('.page-slide-right-leave-active .oa-workspace > .oa-chat-root::before');

    // Reduced motion support for inner container
    expect(surfacesScss).toContain('@media (prefers-reduced-motion: reduce)');

    // Vertical twitch (oaRise jump) suppressed on workspace elements
    expect(surfacesScss).not.toContain('.oa-workspace > .oa-header,\n.oa-workspace > .oa-chat-root');
    expect(surfacesScss).not.toMatch(/\.oa-workspace\s*>\s*\.oa-header[^{]*oaRise/);
    expect(surfacesScss).not.toMatch(/\.oa-admin-rail[^{]*oaRise/);
  });

  it('hides left chat history rail when opening terminal', async () => {
    const fs = await import('node:fs');
    const path = await import('node:path');
    const terminalScssPath = path.resolve(__dirname, '../src/styles/_terminal.scss');
    const terminalScss = fs.readFileSync(terminalScssPath, 'utf8');

    // SCSS guarantees chat sidebar is completely hidden when workspace is in terminal mode
    expect(terminalScss).toContain('.oa-chat-root.is-terminal .ai-chat-sidebar');
    expect(terminalScss).toContain('display: none !important');

    // Surfaces SCSS rotates back chevron to point left
    const surfacesScssPath = path.resolve(__dirname, '../src/styles/_surfaces.scss');
    const surfacesScss = fs.readFileSync(surfacesScssPath, 'utf8');
    expect(surfacesScss).toContain('.oa-terminal-back svg');
    expect(surfacesScss).toContain('transform: rotate(90deg)');
  });

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


