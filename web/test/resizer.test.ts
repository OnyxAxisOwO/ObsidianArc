import { afterEach, describe, expect, it } from 'vitest';
import { createApp, h, type App } from 'vue';
import OaResizer from '../src/components/OaResizer.vue';

let app: App | undefined;
afterEach(() => {
  app?.unmount();
  app = undefined;
  // The component writes to <body>; a test that fails partway through must not
  // leave that behind for the next one.
  document.body.classList.remove('oa-resizing');
  localStorage.clear();
});

/**
 * jsdom has no pointer capture, so the handle gets the three methods the
 * component calls. `captured` is exposed because more than one of the ways a
 * drag ends depends on whether the capture is still held when it does.
 */
// Not mountComponent: the tests use the screen the moment it is mounted, without awaiting, and read its first render.
function mount(): { handle: HTMLElement; captured: () => boolean; drop: () => void } {
  const host = document.createElement('div');
  document.body.append(host);
  app = createApp({
    render: () => h(OaResizer, {
      edge: 'left',
      cssVariable: '--test-width',
      styleTarget: null,
      storageKey: 'test-width',
      min: 100,
      max: 400,
      fallback: 200,
      label: 'Resize the panel',
    }),
  });
  app.mount(host);

  const handle = host.querySelector<HTMLElement>('.oa-resizer')!;
  let captured = false;
  handle.setPointerCapture = () => { captured = true; };
  handle.releasePointerCapture = () => { captured = false; };
  handle.hasPointerCapture = () => captured;
  return { handle, captured: () => captured, drop: () => { captured = false; } };
}

describe('the resizing class OaResizer puts on the body', () => {
  it('comes off on a normal release', () => {
    const { handle } = mount();

    handle.dispatchEvent(new PointerEvent('pointerdown', { bubbles: true, button: 0, pointerId: 1 }));
    expect(document.body.classList.contains('oa-resizing')).toBe(true);

    handle.dispatchEvent(new PointerEvent('pointerup', { bubbles: true, pointerId: 1 }));
    expect(document.body.classList.contains('oa-resizing')).toBe(false);
  });

  // Dragging a panel edge and having the panel close under the pointer — an
  // Escape, or a navigation — used to leave the class on the body, where it
  // blocks text selection and holds the col-resize cursor for the rest of the
  // session, because the only thing that removed it was a release the handle
  // was no longer there to receive.
  it('comes off when the handle is unmounted mid-drag', () => {
    const { handle } = mount();

    handle.dispatchEvent(new PointerEvent('pointerdown', { bubbles: true, button: 0, pointerId: 1 }));
    expect(document.body.classList.contains('oa-resizing')).toBe(true);

    app?.unmount();
    app = undefined;
    expect(document.body.classList.contains('oa-resizing')).toBe(false);
  });

  // A cancel can arrive with the capture already gone. The class has to come
  // off either way: whether the pointer is still captured says nothing about
  // whether the drag is over.
  it('comes off on a cancel whose capture has already been released', () => {
    const { handle, drop } = mount();

    handle.dispatchEvent(new PointerEvent('pointerdown', { bubbles: true, button: 0, pointerId: 1 }));
    expect(document.body.classList.contains('oa-resizing')).toBe(true);

    drop();
    handle.dispatchEvent(new PointerEvent('pointercancel', { bubbles: true, pointerId: 1 }));
    expect(document.body.classList.contains('oa-resizing')).toBe(false);
  });
});
