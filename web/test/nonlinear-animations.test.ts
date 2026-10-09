import { describe, it, expect, beforeEach, afterEach, vi } from 'vitest';
import { createApp, h, nextTick, type App } from 'vue';
import { createMemoryHistory, createRouter } from 'vue-router';
import UsageBoard from '../src/views/admin/usage/UsageBoard.vue';
import AdminDashboard from '../src/views/admin/AdminDashboard.vue';
import OaMenu from '../src/components/OaMenu.vue';
import { adminApi } from '../src/admin/api';
import { provideAdminView } from '../src/views/admin/adminView';
import { dashboardFixture } from './fixtures/dashboard';

let app: App | null = null;
let host: HTMLElement;

beforeEach(() => {
  document.body.textContent = '';
  host = document.createElement('div');
  document.body.appendChild(host);
});

afterEach(() => {
  app?.unmount();
  app = null;
  document.body.textContent = '';
});

describe('Non-linear animation & expand/collapse transitions', () => {
  it('UsageBoard lists every row in one scrolling frame, with no show-all toggle', async () => {
    const rows = Array.from({ length: 10 }, (_, i) => ({
      key: `model-${i}`,
      label: `Model ${i}`,
      requests: 100 - i,
      total_tokens: 1000 * (10 - i),
      errors: 0,
    }));
    const mount = (list: typeof rows) => {
      app?.unmount();
      host.textContent = '';
      app = createApp({
        render() {
          return h(UsageBoard as any, { rows: list, kind: 'model', metric: 'requests', limit: 6, emptyText: 'No models' });
        },
      });
      app.mount(host);
    };

    mount(rows);
    await nextTick();

    // All ten are in the list at once; `limit` only sets how tall the frame is.
    expect(host.querySelectorAll('.oa-board > li').length).toBe(10);
    expect(host.querySelector('.oa-board-more')).toBeNull();
    expect(host.querySelector('.oa-board-collapse')).toBeNull();
    expect((host.querySelector('.oa-board-frame') as HTMLElement).style.getPropertyValue('--board-rows')).toBe('6');

    // It says how many there are, and fades at whichever edge has more past it.
    expect(host.querySelector('.oa-board-count')?.textContent).toContain('10');
    const scroller = host.querySelector<HTMLElement>('.oa-board-scroll')!;
    expect(scroller.classList.contains('fade-bottom')).toBe(true);
    expect(scroller.classList.contains('fade-top')).toBe(false);
    Object.defineProperty(scroller, 'scrollHeight', { value: 700, configurable: true });
    Object.defineProperty(scroller, 'clientHeight', { value: 400, configurable: true });
    scroller.scrollTop = 300;
    scroller.dispatchEvent(new Event('scroll'));
    await nextTick();
    expect(scroller.classList.contains('fade-bottom')).toBe(false);
    expect(scroller.classList.contains('fade-top')).toBe(true);

    // A list that fits is just a list: nothing to count, nothing to fade.
    mount(rows.slice(0, 4));
    await nextTick();
    expect(host.querySelectorAll('.oa-board > li').length).toBe(4);
    expect(host.querySelector('.oa-board-count')).toBeNull();
    expect(host.querySelector('.oa-board-scroll')?.className).toBe('oa-board-scroll');
  });

  it('AdminDashboard ranking details expands and collapses with non-linear animation and rotating chevron', async () => {
    vi.spyOn(adminApi, 'dashboard').mockResolvedValue(dashboardFixture());
    const router = createRouter({ history: createMemoryHistory(), routes: [{ path: '/:pathMatch(.*)*', component: { render: () => null } }] });
    const actions = document.createElement('div');
    app = createApp({
      setup() {
        provideAdminView({ actionsHost: actions, setTitle() {}, reload() {}, params: [] });
        return () => h(AdminDashboard);
      },
    });
    app.use(router);
    app.mount(host);
    await new Promise((r) => setTimeout(r, 0));
    await nextTick();

    const ranking = host.querySelector('#secBusiestModels')!;
    expect(ranking).not.toBeNull();

    const details = ranking.querySelector('details.oa-dashboard-ranking-details');
    expect(details).not.toBeNull();

    const summary = details?.querySelector('summary.oa-dashboard-details-summary');
    expect(summary).not.toBeNull();

    const chevron = summary?.querySelector('.oa-dashboard-details-chevron');
    expect(chevron).not.toBeNull();
    expect(chevron?.classList.contains('open')).toBe(false);

    const collapse = details?.querySelector('.oa-dashboard-details-collapse');
    expect(collapse).not.toBeNull();
    expect(collapse?.classList.contains('open')).toBe(false);

    // Click summary to expand
    (summary as HTMLElement).click();
    await nextTick();

    expect(chevron?.classList.contains('open')).toBe(true);
    expect(collapse?.classList.contains('open')).toBe(true);

    // Click summary again to collapse
    (summary as HTMLElement).click();
    await nextTick();

    expect(chevron?.classList.contains('open')).toBe(false);
    expect(collapse?.classList.contains('open')).toBe(false);
  });

  it('OaMenu mounts and triggers open animation with non-linear transform', async () => {
    app = createApp({
      render() {
        return h(OaMenu as any, null, {
          trigger: ({ toggle }: any) => h('button', { class: 'trigger-btn', onClick: toggle }, 'Menu'),
          default: () => h('div', { class: 'menu-content' }, 'Content'),
        });
      },
    });
    app.mount(host);
    await nextTick();

    // Menu panel is not mounted before trigger is clicked
    expect(host.querySelector('.oa-menu')).toBeNull();

    // Click trigger to open
    const trigger = host.querySelector<HTMLButtonElement>('.trigger-btn')!;
    trigger.click();
    await nextTick();

    // Menu panel should be mounted
    const panel = host.querySelector<HTMLElement>('.oa-menu');
    expect(panel).not.toBeNull();

    // requestAnimationFrame transitions shown to true
    await new Promise((r) => requestAnimationFrame(r));
    await nextTick();
    expect(panel?.classList.contains('open')).toBe(true);
  });

  it('OaMenu shifts right and anchors transform-origin to trigger when overflowing on mobile', async () => {
    // Simulate iPhone viewport: width = 390
    vi.spyOn(window, 'innerWidth', 'get').mockReturnValue(390);

    const menuRef = { value: null as any };
    app = createApp({
      render() {
        return h(OaMenu as any, { ref: (r: any) => { menuRef.value = r; } }, {
          trigger: ({ toggle }: any) => h('button', { class: 'bell-btn', onClick: toggle }, 'Bell'),
          default: () => h('div', { class: 'menu-content' }, 'Announcements'),
        });
      },
    });
    app.mount(host);
    await nextTick();

    const trigger = host.querySelector<HTMLButtonElement>('.bell-btn')!;
    const group = host.querySelector<HTMLElement>('.oa-chip-group')!;

    // Mock trigger group position in the middle of a phone header (e.g. Bell icon at x=176..212)
    vi.spyOn(group, 'getBoundingClientRect').mockReturnValue({
      left: 176,
      right: 212,
      top: 10,
      bottom: 46,
      width: 36,
      height: 36,
      x: 176,
      y: 10,
      toJSON: () => {},
    });

    trigger.click();
    await nextTick();

    const panel = host.querySelector<HTMLElement>('.oa-menu')!;
    expect(panel).not.toBeNull();

    // Mock menu width of 280px (.oa-menu-announce min-width)
    Object.defineProperty(panel, 'offsetWidth', { configurable: true, value: 280 });

    menuRef.value.fit();

    // Default right: 0 would put left at 212 - 280 = -68px (overflows screen by 76px).
    // Shifting right by 76px brings left to GUTTER (8px).
    // With right: -76px, the menu is fully on-screen.
    expect(panel.style.right).toBe('-76px');
    expect(panel.style.left).toBe('auto');

    // Trigger center is at 176 + 18 = 194. Menu left is at 8.
    // Origin is at 194 - 8 = 186px from menu left.
    expect(panel.style.transformOrigin).toBe('186px top');
  });

  it('OaMenu clamps maxWidth to available room on ultra-narrow viewports', async () => {
    // Ultra narrow viewport (e.g. 260px)
    vi.spyOn(window, 'innerWidth', 'get').mockReturnValue(260);

    const menuRef = { value: null as any };
    app = createApp({
      render() {
        return h(OaMenu as any, { ref: (r: any) => { menuRef.value = r; } }, {
          trigger: ({ toggle }: any) => h('button', { class: 'trigger-btn', onClick: toggle }, 'Trigger'),
          default: () => h('div', { class: 'menu-content' }, 'Content'),
        });
      },
    });
    app.mount(host);
    await nextTick();

    const trigger = host.querySelector<HTMLButtonElement>('.trigger-btn')!;
    trigger.click();
    await nextTick();

    const panel = host.querySelector<HTMLElement>('.oa-menu')!;
    Object.defineProperty(panel, 'offsetWidth', { configurable: true, value: 280 });

    menuRef.value.fit();

    // room = 260 - 2 * 8 = 244px
    expect(panel.style.maxWidth).toBe('244px');
    expect(panel.style.minWidth).toBe('0px');
  });

  it('OaMenu does not flap maxWidth/minWidth on subsequent fit() calls when clamped to room', async () => {
    vi.spyOn(window, 'innerWidth', 'get').mockReturnValue(260);

    const menuRef = { value: null as any };
    app = createApp({
      render() {
        return h(OaMenu as any, { ref: (r: any) => { menuRef.value = r; } }, {
          trigger: ({ toggle }: any) => h('button', { class: 'trigger-btn', onClick: toggle }, 'Trigger'),
          default: () => h('div', { class: 'menu-content' }, 'Content'),
        });
      },
    });
    app.mount(host);
    await nextTick();

    const trigger = host.querySelector<HTMLButtonElement>('.trigger-btn')!;
    trigger.click();
    await nextTick();

    const panel = host.querySelector<HTMLElement>('.oa-menu')!;
    Object.defineProperty(panel, 'offsetWidth', { configurable: true, value: 280 });

    menuRef.value.fit();
    expect(panel.style.maxWidth).toBe('244px');
    expect(panel.style.minWidth).toBe('0px');

    // Simulate browser updating offsetWidth after style.maxWidth is set
    Object.defineProperty(panel, 'offsetWidth', { configurable: true, value: 244 });

    // Calling fit() again (e.g. from ResizeObserver) must not wipe out maxWidth
    menuRef.value.fit();
    expect(panel.style.maxWidth).toBe('244px');
    expect(panel.style.minWidth).toBe('0px');
  });

  it('OaMenu anchors transformOrigin to bottom when menu has oa-menu-up', async () => {
    vi.spyOn(window, 'innerWidth', 'get').mockReturnValue(390);

    const menuRef = { value: null as any };
    app = createApp({
      render() {
        return h(OaMenu as any, { ref: (r: any) => { menuRef.value = r; }, menuClass: 'oa-menu-up' }, {
          trigger: ({ toggle }: any) => h('button', { class: 'up-btn', onClick: toggle }, 'Up'),
          default: () => h('div', { class: 'menu-content' }, 'Content'),
        });
      },
    });
    app.mount(host);
    await nextTick();

    const trigger = host.querySelector<HTMLButtonElement>('.up-btn')!;
    const group = host.querySelector<HTMLElement>('.oa-chip-group')!;

    vi.spyOn(group, 'getBoundingClientRect').mockReturnValue({
      left: 176,
      right: 212,
      top: 500,
      bottom: 536,
      width: 36,
      height: 36,
      x: 176,
      y: 500,
      toJSON: () => {},
    });

    trigger.click();
    await nextTick();

    const panel = host.querySelector<HTMLElement>('.oa-menu')!;
    Object.defineProperty(panel, 'offsetWidth', { configurable: true, value: 280 });

    menuRef.value.fit();

    expect(panel.style.right).toBe('-76px');
    // Vertical origin must be bottom for upward opening menu
    expect(panel.style.transformOrigin).toBe('186px bottom');
  });

  it('OaMenu with oa-menu-left shifts left when overflowing right screen boundary', async () => {
    vi.spyOn(window, 'innerWidth', 'get').mockReturnValue(390);

    const menuRef = { value: null as any };
    app = createApp({
      render() {
        return h(OaMenu as any, { ref: (r: any) => { menuRef.value = r; }, menuClass: 'oa-menu-left' }, {
          trigger: ({ toggle }: any) => h('button', { class: 'left-btn', onClick: toggle }, 'Left'),
          default: () => h('div', { class: 'menu-content' }, 'Content'),
        });
      },
    });
    app.mount(host);
    await nextTick();

    const trigger = host.querySelector<HTMLButtonElement>('.left-btn')!;
    const group = host.querySelector<HTMLElement>('.oa-chip-group')!;

    // Trigger near the right edge: left = 200, right = 236
    vi.spyOn(group, 'getBoundingClientRect').mockReturnValue({
      left: 200,
      right: 236,
      top: 10,
      bottom: 46,
      width: 36,
      height: 36,
      x: 200,
      y: 10,
      toJSON: () => {},
    });

    trigger.click();
    await nextTick();

    const panel = host.querySelector<HTMLElement>('.oa-menu')!;
    Object.defineProperty(panel, 'offsetWidth', { configurable: true, value: 280 });

    menuRef.value.fit();

    // unscaledLeft = 200, unscaledRight = 200 + 280 = 480.
    // viewWidth - GUTTER = 390 - 8 = 382.
    // shift = 382 - 480 = -98px.
    expect(panel.style.left).toBe('-98px');
    expect(panel.style.right).toBe('auto');

    // triggerCenter = 200 + 18 = 218. menuLeft = 200 + (-98) = 102.
    // originX = 218 - 102 = 116px.
    expect(panel.style.transformOrigin).toBe('116px top');
  });

  it('OaMenu resets offsets and releases maxWidth when room expands to >= 320px', async () => {
    // Start narrow
    const innerWidthSpy = vi.spyOn(window, 'innerWidth', 'get').mockReturnValue(260);

    const menuRef = { value: null as any };
    app = createApp({
      render() {
        return h(OaMenu as any, { ref: (r: any) => { menuRef.value = r; } }, {
          trigger: ({ toggle }: any) => h('button', { class: 'trigger-btn', onClick: toggle }, 'Trigger'),
          default: () => h('div', { class: 'menu-content' }, 'Content'),
        });
      },
    });
    app.mount(host);
    await nextTick();

    const trigger = host.querySelector<HTMLButtonElement>('.trigger-btn')!;
    const group = host.querySelector<HTMLElement>('.oa-chip-group')!;

    vi.spyOn(group, 'getBoundingClientRect').mockReturnValue({
      left: 500,
      right: 536,
      top: 10,
      bottom: 46,
      width: 36,
      height: 36,
      x: 500,
      y: 10,
      toJSON: () => {},
    });

    trigger.click();
    await nextTick();

    const panel = host.querySelector<HTMLElement>('.oa-menu')!;
    Object.defineProperty(panel, 'offsetWidth', { configurable: true, value: 244 });

    menuRef.value.fit();
    expect(panel.style.maxWidth).toBe('244px');

    // Window expands to wide screen
    innerWidthSpy.mockReturnValue(1000);
    Object.defineProperty(panel, 'offsetWidth', { configurable: true, value: 280 });

    menuRef.value.fit();
    expect(panel.style.maxWidth).toBe('');
    expect(panel.style.minWidth).toBe('');
    expect(panel.style.left).toBe('');
    expect(panel.style.right).toBe('');
    expect(panel.style.transformOrigin).toBe('');
  });
});
