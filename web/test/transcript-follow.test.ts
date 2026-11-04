// Following an answer as it streams, and letting go when the reader scrolls
// away from it.
//
// The rule is old: yanking the view back to the bottom while somebody reads
// an earlier part of the answer is worse than letting the answer run off
// screen. What this pins is the mechanism, because it broke once without a
// visible trace — a `.passive` modifier on the scroll area's component
// listener compiled to a handler Vue's emit never looks up, so the "reader
// scrolled away" signal never arrived and every streamed token pulled the
// view back down.
//
// jsdom has no layout, so the scroller's geometry is stubbed on the element.

import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import { createApp, defineComponent, h, nextTick, type App } from 'vue';

const stub = defineComponent({ render: () => h('div') });
vi.mock('@/chat/ChatComposer.vue', () => ({ default: stub }));
vi.mock('@/chat/ChatChallenge.vue', () => ({ default: stub }));
vi.mock('@/chat/ChatSidebar.vue', () => ({ default: stub }));
vi.mock('@/chat/ChatPending.vue', () => ({ default: stub }));
vi.mock('@/chat/ChatMessage.vue', () => ({ default: stub }));

const { busy, messages, scrollTick } = await import('@/chat/useChat');
const { default: ChatSurface } = await import('@/chat/ChatSurface.vue');

let app: App | null = null;
let host: HTMLElement;

beforeEach(() => {
  host = document.createElement('div');
  document.body.appendChild(host);
});

afterEach(() => {
  app?.unmount();
  app = null;
  busy.value = false;
  messages.value = [];
});

/** Mounts the surface and returns its scroller, 2000px tall in a 500px view. */
async function mountScroller(): Promise<{ node: HTMLElement; top: () => number }> {
  app = createApp({ render: () => h(ChatSurface) });
  app.mount(host);
  messages.value = [{ id: 'm1' } as never];
  await nextTick();

  const node = host.querySelector<HTMLElement>('.ai-chat-scroll')!;
  let scrollTop = 0;
  Object.defineProperty(node, 'scrollHeight', { configurable: true, get: () => 2000 });
  Object.defineProperty(node, 'clientHeight', { configurable: true, get: () => 500 });
  Object.defineProperty(node, 'scrollTop', {
    configurable: true,
    get: () => scrollTop,
    set: (value: number) => { scrollTop = Math.min(value, 1500); },
  });
  return { node, top: () => scrollTop };
}

async function tick(): Promise<void> {
  scrollTick.value += 1;
  await nextTick();
  await nextTick();
}

describe('the transcript while an answer streams', () => {
  it('keeps following when the reader is at the bottom', async () => {
    const { node, top } = await mountScroller();
    busy.value = true;
    node.scrollTop = 1500;
    node.dispatchEvent(new Event('scroll'));

    await tick();
    expect(top()).toBe(1500);
  });

  it('stops pulling the view down once the reader scrolls up', async () => {
    const { node, top } = await mountScroller();
    busy.value = true;
    node.scrollTop = 300;
    node.dispatchEvent(new Event('scroll'));

    await tick();
    expect(top()).toBe(300);
  });
});
