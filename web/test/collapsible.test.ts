// A section of a panel that folds. What is held here is that the heading is a
// real button that folds and only that — the controls beside it are its
// neighbours, not its contents — that a folded section is inert rather than only
// out of sight, that the reader's choice is remembered per section and survives
// a remount, and that a control which needs the section open can open it without
// overwriting the reader's choice.

import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import { createApp, defineComponent, h, nextTick, ref, type App } from 'vue';
import OaCollapsible from '@/components/OaCollapsible.vue';

let app: App | null = null;
let host: HTMLElement;

beforeEach(() => {
  localStorage.clear();
  host = document.createElement('div');
  document.body.appendChild(host);
});

afterEach(() => {
  app?.unmount();
  app = null;
  document.body.textContent = '';
  vi.restoreAllMocks();
});

async function settle(): Promise<void> {
  await nextTick();
  await nextTick();
}

interface Handle { reveal(): void; toggle(): void; expanded: boolean }

/** Mounts one or more sections; returns the first one's exposed handle. */
async function mount(options: { id?: string; open?: boolean; extra?: boolean } = {}): Promise<{ handle: () => Handle; clicks: () => number }> {
  const inner = ref<Handle | null>(null);
  let clicks = 0;
  app = createApp(defineComponent({
    render: () => h('div', [
      h(OaCollapsible, {
        ref: inner,
        id: options.id ?? 'alpha',
        title: 'Alpha',
        ...(options.open === undefined ? {} : { open: options.open }),
      }, {
        actions: () => h('button', { class: 'beside', type: 'button', onClick: () => { clicks += 1; } }, 'Beside'),
        default: () => h('button', { class: 'inside', type: 'button' }, 'Inside'),
      }),
      ...(options.extra ? [h(OaCollapsible, { id: 'beta', title: 'Beta' }, { default: () => h('p', 'Second') })] : []),
    ]),
  }));
  app.mount(host);
  await settle();
  return { handle: () => inner.value as unknown as Handle, clicks: () => clicks };
}

function toggle(title = 'Alpha'): HTMLButtonElement {
  const found = [...host.querySelectorAll<HTMLButtonElement>('.oa-collapsible-toggle')].find((node) => node.textContent?.includes(title));
  if (!found) throw new Error(`No section called ${title}`);
  return found;
}

function bodyOf(title = 'Alpha'): HTMLElement {
  const id = toggle(title).getAttribute('aria-controls')!;
  return host.querySelector<HTMLElement>(`[id="${id}"]`)!;
}

describe('OaCollapsible', () => {
  it('starts open, with a heading that is one button naming the body it folds', async () => {
    await mount();
    const button = toggle();
    expect(button.getAttribute('aria-expanded')).toBe('true');
    expect(bodyOf().hasAttribute('inert')).toBe(false);
    // The button is what is in the heading: the title is text inside it, and
    // the heading itself is a heading, for the outline of the panel.
    expect(button.closest('h3')).not.toBeNull();
    expect(button.textContent).toContain('Alpha');
  });

  it('folds and unfolds, and a folded body is inert rather than only hidden', async () => {
    await mount();
    toggle().click();
    await settle();
    expect(toggle().getAttribute('aria-expanded')).toBe('false');
    expect(bodyOf().hasAttribute('inert')).toBe(true);
    expect(host.querySelector('.oa-collapsible')!.classList.contains('open')).toBe(false);

    toggle().click();
    await settle();
    expect(toggle().getAttribute('aria-expanded')).toBe('true');
    expect(bodyOf().hasAttribute('inert')).toBe(false);
  });

  it('keeps a control beside the heading out of the button, so pressing it folds nothing', async () => {
    const { clicks } = await mount();
    const beside = host.querySelector<HTMLButtonElement>('.beside')!;
    expect(toggle().contains(beside)).toBe(false);
    expect(beside.closest('.oa-collapsible-head')).not.toBeNull();

    beside.click();
    await settle();
    expect(clicks()).toBe(1);
    expect(toggle().getAttribute('aria-expanded')).toBe('true');
  });

  it('remembers what the reader chose, per section, across a remount', async () => {
    await mount({ extra: true });
    toggle().click();
    await settle();
    expect(localStorage.getItem('obsidian-arc-fold-alpha')).toBe('1');
    expect(localStorage.getItem('obsidian-arc-fold-beta')).toBeNull();

    app?.unmount();
    app = null;
    host.textContent = '';
    await mount({ extra: true });
    expect(toggle('Alpha').getAttribute('aria-expanded')).toBe('false');
    // Its neighbour was never touched.
    expect(toggle('Beta').getAttribute('aria-expanded')).toBe('true');

    toggle().click();
    await settle();
    expect(localStorage.getItem('obsidian-arc-fold-alpha')).toBe('0');
  });

  it('starts as the caller says when nothing is remembered, and as the reader left it when something is', async () => {
    await mount({ open: false });
    expect(toggle().getAttribute('aria-expanded')).toBe('false');
    app?.unmount();
    app = null;
    host.textContent = '';

    localStorage.setItem('obsidian-arc-fold-alpha', '0');
    await mount({ open: false });
    expect(toggle().getAttribute('aria-expanded')).toBe('true');
  });

  it('can be opened by a control that needs it, without overwriting the reader’s choice', async () => {
    localStorage.setItem('obsidian-arc-fold-alpha', '1');
    const { handle } = await mount();
    expect(toggle().getAttribute('aria-expanded')).toBe('false');

    handle().reveal();
    await settle();
    expect(toggle().getAttribute('aria-expanded')).toBe('true');
    expect(localStorage.getItem('obsidian-arc-fold-alpha')).toBe('1');

    // The next visit is still the way they left it.
    app?.unmount();
    app = null;
    host.textContent = '';
    await mount();
    expect(toggle().getAttribute('aria-expanded')).toBe('false');
  });

  it('works without storage: it starts open and still folds for the page', async () => {
    vi.spyOn(Storage.prototype, 'getItem').mockImplementation(() => { throw new Error('denied'); });
    vi.spyOn(Storage.prototype, 'setItem').mockImplementation(() => { throw new Error('denied'); });
    await mount();
    expect(toggle().getAttribute('aria-expanded')).toBe('true');
    toggle().click();
    await settle();
    expect(toggle().getAttribute('aria-expanded')).toBe('false');
  });
});
