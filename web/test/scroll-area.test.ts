import { afterEach, expect, it } from 'vitest';
import { createApp, h, nextTick, ref, type App } from 'vue';
import OaScrollArea from '../src/components/OaScrollArea.vue';

let app: App | undefined;
afterEach(() => {
  app?.unmount();
  document.body.textContent = '';
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
