import { describe, it, expect, beforeEach, afterEach } from 'vitest';
import { createApp, h, ref, type App } from 'vue';
import OaSelect from '../src/components/OaSelect.vue';
import { placeList } from '../src/lib/select-placement';

// jsdom has no layout, so neither of these exists. The control calls both
// while opening; what is under test is everything around them.
let app: App | null = null;
let host: HTMLElement;

beforeEach(() => {
  Element.prototype.scrollIntoView = () => {};
  document.body.textContent = '';
  host = document.createElement('div');
  document.body.appendChild(host);
});

afterEach(() => {
  app?.unmount();
  app = null;
  // The open list is module state, so a test that left one open would be read
  // by the next one as a list it opened itself.
  document.dispatchEvent(new PointerEvent('pointerdown'));
  document.body.textContent = '';
});

const COLOURS = [
  { value: 'red', label: 'Red' },
  { value: 'green', label: 'Green' },
  { value: 'blue', label: 'Blue' },
];

interface Mounted {
  trigger: HTMLButtonElement;
  value(): string;
  changes: string[];
}

function mount(choices = COLOURS, initial = 'red'): Mounted {
  const value = ref(initial);
  const changes: string[] = [];

  app = createApp({
    render: () => h(OaSelect, {
      choices,
      modelValue: value.value,
      'onUpdate:modelValue': (next: string) => {
        value.value = next;
        changes.push(next);
      },
    }),
  });
  app.mount(host);

  const trigger = host.querySelector<HTMLButtonElement>('.oa-select');
  if (!trigger) throw new Error('no trigger rendered');
  return { trigger, value: () => value.value, changes };
}

function list(): HTMLElement | null {
  return document.querySelector<HTMLElement>('.oa-select-menu');
}

async function settle(): Promise<void> {
  // A tick for Vue, and a frame for the transition class the control adds one
  // frame after opening.
  await Promise.resolve();
  await new Promise((resolve) => requestAnimationFrame(resolve));
  await Promise.resolve();
}

describe('the option list', () => {
  it('shows the label of the current value on the trigger', () => {
    const control = mount();
    expect(control.trigger.querySelector('.oa-select-label')?.textContent).toBe('Red');
  });

  it('opens on click and lands on <body>, out of every clipping ancestor', async () => {
    const control = mount();
    control.trigger.click();
    await settle();

    expect(list()).not.toBeNull();
    expect(list()?.parentElement).toBe(document.body);
    expect(control.trigger.getAttribute('aria-expanded')).toBe('true');
  });

  it('is a combobox pointing at a listbox of options', async () => {
    const control = mount();
    expect(control.trigger.getAttribute('role')).toBe('combobox');
    control.trigger.click();
    await settle();

    expect(list()?.getAttribute('role')).toBe('listbox');
    const options = list()!.querySelectorAll('[role="option"]');
    expect(options).toHaveLength(3);
    expect(options[0]?.getAttribute('aria-selected')).toBe('true');
    // Focus stays on the trigger; the active row is named rather than focused.
    expect(control.trigger.getAttribute('aria-activedescendant')).toBe(options[0]?.id);
  });

  it('reports a choice and closes on it', async () => {
    const control = mount();
    control.trigger.click();
    await settle();

    list()!.querySelectorAll<HTMLElement>('.oa-menu-item')[2]!.click();
    await settle();

    expect(control.value()).toBe('blue');
    expect(control.changes).toEqual(['blue']);
    expect(control.trigger.getAttribute('aria-expanded')).toBe('false');
  });

  it('does not report a choice that changes nothing', async () => {
    const control = mount();
    control.trigger.click();
    await settle();

    list()!.querySelectorAll<HTMLElement>('.oa-menu-item')[0]!.click();
    await settle();

    expect(control.changes).toEqual([]);
  });

  it('walks with the arrow keys and commits on Enter', async () => {
    const control = mount();
    control.trigger.dispatchEvent(new KeyboardEvent('keydown', { key: 'ArrowDown', bubbles: true }));
    await settle();
    expect(list()).not.toBeNull();

    control.trigger.dispatchEvent(new KeyboardEvent('keydown', { key: 'ArrowDown', bubbles: true }));
    await settle();
    control.trigger.dispatchEvent(new KeyboardEvent('keydown', { key: 'Enter', bubbles: true }));
    await settle();

    expect(control.value()).toBe('green');
  });

  it('closes on Escape without choosing', async () => {
    const control = mount();
    control.trigger.click();
    await settle();

    control.trigger.dispatchEvent(new KeyboardEvent('keydown', { key: 'Escape', bubbles: true }));
    await settle();

    expect(control.trigger.getAttribute('aria-expanded')).toBe('false');
    expect(control.changes).toEqual([]);
  });

  it('closes when the pointer goes down outside it', async () => {
    const control = mount();
    control.trigger.click();
    await settle();

    document.dispatchEvent(new PointerEvent('pointerdown', { bubbles: true }));
    await settle();

    expect(control.trigger.getAttribute('aria-expanded')).toBe('false');
  });

  it('jumps to a choice by typing its first letter', async () => {
    const control = mount();
    control.trigger.dispatchEvent(new KeyboardEvent('keydown', { key: 'b', bubbles: true }));
    await settle();

    // Closed, so a letter commits rather than only moving the highlight.
    expect(control.value()).toBe('blue');
  });

  it('forwards caller classes to the trigger button', () => {
    app?.unmount();
    app = createApp({
      render: () => h(OaSelect, {
        choices: COLOURS,
        modelValue: 'red',
        class: 'oa-filter-select',
      }),
    });
    app.mount(host);

    const trigger = host.querySelector<HTMLButtonElement>('.oa-select');
    expect(trigger?.classList.contains('oa-filter-select')).toBe(true);
  });

  it('wraps choices in an overlay scroll area with track and thumb', async () => {
    const manyChoices = Array.from({ length: 15 }, (_, i) => ({ value: `v${i}`, label: `Option ${i}` }));
    const control = mount(manyChoices, 'v0');
    control.trigger.click();
    await settle();

    const menu = list();
    expect(menu).not.toBeNull();
    const wrap = menu?.querySelector('.oa-select-scroll-wrap');
    const scroller = menu?.querySelector('.oa-select-scroll');
    const track = menu?.querySelector('.oa-overlay-track');
    const thumb = menu?.querySelector('.oa-overlay-thumb');

    expect(wrap).not.toBeNull();
    expect(scroller).not.toBeNull();
    expect(track).not.toBeNull();
    expect(thumb).not.toBeNull();
  });

  it('scrolls the overlay scroller when walking down choices', async () => {
    const manyChoices = Array.from({ length: 10 }, (_, i) => ({ value: `v${i}`, label: `Option ${i}` }));
    const control = mount(manyChoices, 'v0');
    control.trigger.click();
    await settle();

    const scroller = list()?.querySelector<HTMLElement>('.oa-select-scroll');
    expect(scroller).not.toBeNull();

    Object.defineProperty(scroller, 'clientHeight', { value: 100, configurable: true });
    Object.defineProperty(scroller, 'scrollHeight', { value: 300, configurable: true });
    Array.from(scroller!.children).forEach((child, i) => {
      Object.defineProperty(child, 'offsetTop', { value: i * 30, configurable: true });
      Object.defineProperty(child, 'offsetHeight', { value: 30, configurable: true });
    });

    for (let i = 0; i < 5; i++) {
      control.trigger.dispatchEvent(new KeyboardEvent('keydown', { key: 'ArrowDown', bubbles: true }));
      await settle();
    }

    expect(scroller!.scrollTop).toBeGreaterThan(0);
  });
});

describe('placeList', () => {
  const view = { width: 1280, height: 800 };
  const box = { top: 100, bottom: 130, left: 200, width: 180 };

  it('sits under the trigger when there is room', () => {
    const spot = placeList(box, 120, 180, view);
    expect(spot.top).toBe(box.bottom + 6);
    expect(spot.left).toBe(box.left);
    expect(spot.origin).toBe('top left');
  });

  it('flips above only when that side actually fits more of it', () => {
    const low = { top: 700, bottom: 730, left: 200, width: 180 };
    const spot = placeList(low, 300, 180, view);
    expect(spot.origin).toBe('bottom left');
    expect(spot.top).toBeLessThan(low.top);
  });

  it('clamps to the room on the side it lands on rather than to a fixed height', () => {
    const short = { width: 1280, height: 200 };
    const spot = placeList({ top: 60, bottom: 90, left: 10, width: 100 }, 320, 100, short);
    expect(spot.maxHeight).toBeLessThanOrEqual(short.height);
    expect(spot.top + spot.maxHeight).toBeLessThanOrEqual(short.height);
  });

  it('keeps a wide list inside the right-hand edge', () => {
    const spot = placeList({ top: 100, bottom: 130, left: 1200, width: 60 }, 100, 400, view);
    expect(spot.left).toBe(view.width - 8 - 400);
  });

  it('never places a list off the left-hand edge', () => {
    const narrow = { width: 320, height: 800 };
    const spot = placeList({ top: 100, bottom: 130, left: 4, width: 60 }, 100, 400, narrow);
    expect(spot.left).toBe(8);
  });
});
