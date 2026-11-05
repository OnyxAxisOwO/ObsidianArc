import { describe, it, expect, beforeEach, afterEach, vi } from 'vitest';
import { createApp, h, type Component, nextTick, type App } from 'vue';
import UsageBoard from '../src/views/admin/usage/UsageBoard.vue';
import OaMenu from '../src/components/OaMenu.vue';

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
  vi.restoreAllMocks();
});

describe('scrolling usage boards', () => {
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
          // A defaulted optional prop makes this component's props miss h()'s overload under
          // exactOptionalPropertyTypes, so the call takes the component as a plain Component.
          return h(UsageBoard as Component, { rows: list, kind: 'model', metric: 'requests', limit: 6, emptyText: 'No models' });
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
});

async function openMenu(menuClass = '', up = false, left = 176) {
  const menu = { current: null as InstanceType<typeof OaMenu> | null };
  app = createApp({
    render: () => h(OaMenu, {
      ref: (instance) => { menu.current = instance as InstanceType<typeof OaMenu> | null; },
      menuClass,
      up,
    }, {
      trigger: ({ toggle }: { toggle: () => void }) => h('button', { onClick: toggle }, 'Menu'),
      default: () => h('div', 'Content'),
    }),
  });
  app.mount(host);
  const trigger = host.querySelector<HTMLElement>('.oa-chip-group')!;
  vi.spyOn(trigger, 'getBoundingClientRect').mockReturnValue(new DOMRect(left, 10, 36, 36));
  trigger.querySelector('button')!.click();
  await nextTick();

  const panel = host.querySelector<HTMLElement>('.oa-menu')!;
  const width = (value: number) => Object.defineProperty(panel, 'offsetWidth', { configurable: true, value });
  const fit = () => menu.current!.fit();
  width(280);
  fit();
  return { style: panel.style, width, fit };
}

describe('menu placement', () => {
  it.each([
    { menuClass: '', up: false, origin: '186px top' },
    { menuClass: 'oa-menu-up', up: false, origin: '186px bottom' },
    { menuClass: 'row-menu', up: true, origin: '186px bottom' },
  ])('fits a right-aligned menu and anchors to the trigger ($menuClass)', async ({ menuClass, up, origin }) => {
    vi.spyOn(window, 'innerWidth', 'get').mockReturnValue(390);
    const { style } = await openMenu(menuClass, up);
    expect(style.right).toBe('-76px');
    expect(style.left).toBe('auto');
    expect(style.transformOrigin).toBe(origin);
  });

  it('shifts a left-aligned menu away from the right edge', async () => {
    vi.spyOn(window, 'innerWidth', 'get').mockReturnValue(390);
    const { style } = await openMenu('oa-menu-left', false, 200);
    expect(style.left).toBe('-98px');
    expect(style.right).toBe('auto');
    expect(style.transformOrigin).toBe('116px top');
  });

  it('keeps the width cap across repeated measurements and releases it when the viewport grows', async () => {
    const viewport = vi.spyOn(window, 'innerWidth', 'get').mockReturnValue(260);
    const { style, width, fit } = await openMenu('', false, 500);
    expect(style.maxWidth).toBe('244px');
    expect(style.minWidth).toBe('0px');

    // The next measurement sees the width the cap already set.
    width(244);
    fit();
    expect(style.maxWidth).toBe('244px');
    expect(style.minWidth).toBe('0px');

    viewport.mockReturnValue(1000);
    width(280);
    fit();
    expect(style.maxWidth).toBe('');
    expect(style.minWidth).toBe('');
    expect(style.left).toBe('');
    expect(style.right).toBe('');
    expect(style.transformOrigin).toBe('');
  });
});
