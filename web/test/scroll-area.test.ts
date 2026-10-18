import { afterEach, expect, it } from 'vitest';
import { createApp, h, nextTick, ref, type App } from 'vue';
import OaScrollArea from '../src/components/OaScrollArea.vue';

let app: App | undefined;
afterEach(() => {
  app?.unmount();
  // The drag below writes to <body>; a test that fails partway through must
  // not leave that behind for the next one.
  document.body.style.userSelect = '';
});

it('updates the scrollbar when filtering hides mounted form content', async () => {
  const host = document.createElement('div');
  document.body.append(host);
  const filtered = ref(false);
  app = createApp({ render: () => h(OaScrollArea, { scrollClass: 'test-scroller' }, {
    default: () => h('section', { style: { display: filtered.value ? 'none' : '' } }, 'Settings'),
  }) });
  app.mount(host);
  const scroller = host.querySelector('.test-scroller')!;
  Object.defineProperties(scroller, {
    clientHeight: { get: () => 100 },
    scrollHeight: { get: () => filtered.value ? 100 : 500 },
  });
  await new Promise(requestAnimationFrame);
  await nextTick();
  const track = host.querySelector<HTMLElement>('.oa-overlay-track')!;
  expect(track.style.display).toBe('block');
  const section = host.querySelector('section');
  filtered.value = true;
  await nextTick();
  await new Promise((resolve) => setTimeout(resolve, 0));
  expect(track.style.display).toBe('none');
  expect(host.querySelector('section')).toBe(section);
  filtered.value = false;
  await nextTick();
  await new Promise((resolve) => setTimeout(resolve, 0));
  expect(track.style.display).toBe('block');
});

// Dragging the thumb turns off text selection on <body> and restores it on
// mouseup. The listener that restores it is on the window and is removed with
// the component, so releasing the button after the scroll area has gone — a
// panel closing, or the admin body being replaced, while the mouse is still
// down — left the whole application unable to select text until a reload.
it('restores text selection when it is unmounted in the middle of a drag', async () => {
  const host = document.createElement('div');
  document.body.append(host);
  app = createApp({ render: () => h(OaScrollArea, { scrollClass: 'test-scroller' }, {
    default: () => h('section', 'Settings'),
  }) });
  app.mount(host);
  const scroller = host.querySelector('.test-scroller')!;
  Object.defineProperties(scroller, {
    clientHeight: { get: () => 100 },
    scrollHeight: { get: () => 500 },
  });
  await new Promise(requestAnimationFrame);
  await nextTick();

  const thumb = host.querySelector<HTMLElement>('.oa-overlay-thumb')!;
  expect(thumb, 'the thumb should be drawn for a scrollable area').toBeTruthy();
  thumb.dispatchEvent(new MouseEvent('mousedown', { bubbles: true, button: 0 }));
  expect(document.body.style.userSelect).toBe('none');

  // The button never comes up: the component goes away under it.
  app.unmount();
  app = undefined;
  expect(document.body.style.userSelect).not.toBe('none');
});
