// The streaming caret hangs off the answer's last block, which every paint
// rebuilds. Left alone, each new block restarted the blink and the caret sat on
// its first half for as long as text kept arriving. The phase now comes from
// the page clock, written onto the host as a negative delay, so a rebuilt block
// picks the blink up where the old one was.

import { afterEach, describe, expect, it, vi } from 'vitest';
import { createApp, h, nextTick, ref, type App } from 'vue';
import OaMarkdown from '../src/components/OaMarkdown.vue';

let app: App | null = null;

afterEach(() => {
  app?.unmount();
  app = null;
  vi.restoreAllMocks();
});

// Not mountComponent: a test changes a prop after mounting, and mountComponent's props are fixed.
function mount(text: { value: string }): HTMLElement {
  const root = document.createElement('div');
  document.body.append(root);
  app = createApp({ render: () => h(OaMarkdown, { text: text.value, class: 'ai-answer-streaming' }) });
  app.mount(root);
  const host = root.firstElementChild;
  if (!(host instanceof HTMLElement)) throw new Error('OaMarkdown drew no host');
  return host;
}

describe('the streaming caret', () => {
  it('carries the page clock’s phase on the host after a paint', () => {
    vi.spyOn(performance, 'now').mockReturnValue(12_345);
    const host = mount(ref('Hello'));
    expect(host.style.getPropertyValue('--ai-caret-delay')).toBe('-345ms');
  });

  it('moves the phase on with the clock when the text grows', async () => {
    const now = vi.spyOn(performance, 'now').mockReturnValue(1_000);
    const text = ref('Hello');
    const host = mount(text);
    expect(host.style.getPropertyValue('--ai-caret-delay')).toBe('0ms');

    now.mockReturnValue(2_750);
    text.value = 'Hello, world';
    await nextTick();
    expect(host.textContent).toContain('Hello, world');
    expect(host.style.getPropertyValue('--ai-caret-delay')).toBe('-750ms');
  });
});
