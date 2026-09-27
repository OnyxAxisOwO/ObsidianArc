import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import { createApp, nextTick, type App } from 'vue';
import ChatModeSwitch from '../src/chat/ChatModeSwitch.vue';
import { pendingMode, setMode } from '../src/stores/workspace';

// The drag hangs pointermove/pointerup/pointercancel on window, and the whole
// reason the switch is its own component is so those come off when a
// conversation appears and v-if drops it. These assertions pin the gesture
// (drag settles to the nearer side), the plain click (still toggles), the
// disambiguation (a drag's release does not also toggle), and the cleanup
// (the window listeners are gone once the switch unmounts mid-drag).

let app: App | undefined;

// Every drag adds three window listeners and must remove all three. Recording
// them here lets a test assert the balance without reaching into the
// component — the same shape as resizer.test's body-class check.
let live: Array<{ type: string; fn: unknown }> = [];
const DRAG_EVENTS = ['pointermove', 'pointerup', 'pointercancel'];
function dragListeners(): number {
  return live.filter((l) => DRAG_EVENTS.includes(l.type)).length;
}

beforeEach(() => {
  // The store is a module singleton; a test that left it in work mode would
  // leak into the next one's starting point.
  setMode('chat');
  live = [];
  const add = window.addEventListener.bind(window);
  const remove = window.removeEventListener.bind(window);
  vi.spyOn(window, 'addEventListener').mockImplementation((type, fn, opts) => {
    live.push({ type, fn });
    add(type, fn as EventListener, opts);
  });
  vi.spyOn(window, 'removeEventListener').mockImplementation((type, fn, opts) => {
    const i = live.findIndex((l) => l.type === type && l.fn === fn);
    if (i >= 0) live.splice(i, 1);
    remove(type, fn as EventListener, opts);
  });
});

afterEach(() => {
  app?.unmount();
  app = undefined;
  document.body.textContent = '';
  vi.restoreAllMocks();
});

/**
 * jsdom gives every element a zero-sized box, so the drag geometry would
 * collapse to nothing. Stubbing the strip's rect and the pill's width gives
 * the release a real midpoint to settle either side of: pill 58 + gap 2, so
 * travel is 60 and the halfway point the release compares against is 30.
 */
function mount(): { strip: HTMLElement; buttons: HTMLButtonElement[] } {
  const host = document.createElement('div');
  document.body.append(host);
  app = createApp(ChatModeSwitch);
  app.mount(host);

  const strip = host.querySelector<HTMLElement>('.ai-mode-switch')!;
  const pill = host.querySelector<HTMLElement>('.ai-mode-pill')!;
  strip.getBoundingClientRect = () => ({
    left: 0, top: 0, right: 120, bottom: 30, width: 120, height: 30, x: 0, y: 0, toJSON() {},
  }) as DOMRect;
  Object.defineProperty(pill, 'offsetWidth', { configurable: true, value: 58 });

  const buttons = [...host.querySelectorAll<HTMLButtonElement>('.ai-mode-choice')];
  return { strip, buttons };
}

function down(strip: HTMLElement, clientX: number): void {
  strip.dispatchEvent(new PointerEvent('pointerdown', { bubbles: true, button: 0, clientX, pointerId: 1 }));
}
function moveTo(clientX: number): void {
  window.dispatchEvent(new PointerEvent('pointermove', { clientX, pointerId: 1 }));
}
function up(): void {
  window.dispatchEvent(new PointerEvent('pointerup', { pointerId: 1 }));
}

describe('ChatModeSwitch drag gesture', () => {
  it('settles to the side the pill was dragged nearer to', async () => {
    const { strip } = mount();
    expect(pendingMode.value).toBe('chat');

    down(strip, 10);
    moveTo(120); // past the far edge — the pill is nearest the work side
    await nextTick();
    expect(strip.classList.contains('dragging')).toBe(true);
    up();
    await nextTick();

    expect(pendingMode.value).toBe('work');
    // The transition is handed back to the class transform on release.
    expect(strip.classList.contains('dragging')).toBe(false);
  });

  it('settles back to chat when the pill is dragged to the near side', () => {
    const { strip } = mount();
    setMode('work');

    down(strip, 110);
    moveTo(4); // dragged to the left edge — nearest the chat side
    up();

    expect(pendingMode.value).toBe('chat');
  });

  it('toggles on a plain click, with no drag', () => {
    const { buttons } = mount();
    const work = buttons[1]!;

    work.dispatchEvent(new MouseEvent('click', { bubbles: true }));
    expect(pendingMode.value).toBe('work');
  });

  it('does not toggle again on the click a drag-release fires', () => {
    const { strip, buttons } = mount();

    // A drag that lands on the work side, ending over the chat button.
    down(strip, 10);
    moveTo(120);
    up();
    expect(pendingMode.value).toBe('work');

    // The click the same release fires over the chat button is swallowed, so
    // the mode stays where the drag put it rather than flipping to chat.
    buttons[0]!.dispatchEvent(new MouseEvent('click', { bubbles: true }));
    expect(pendingMode.value).toBe('work');
  });

  it('does not commit a side when a press never moves', () => {
    const { strip } = mount();

    // A press with a jitter inside the slack is a click, not a drag: nothing
    // is committed by the release, and the button's own click decides.
    down(strip, 60);
    moveTo(62);
    up();

    expect(pendingMode.value).toBe('chat');
    expect(strip.classList.contains('dragging')).toBe(false);
  });

  it('drops the window listeners when unmounted mid-drag', () => {
    const { strip } = mount();

    down(strip, 10);
    moveTo(120);
    expect(dragListeners()).toBe(3);

    // The transcript filled and v-if dropped the switch while a drag was held.
    app?.unmount();
    app = undefined;
    expect(dragListeners()).toBe(0);

    // An unmount is not a release the reader chose, so no side was committed;
    // and the stray pointerup that follows finds nothing listening.
    up();
    expect(pendingMode.value).toBe('chat');
  });
});
