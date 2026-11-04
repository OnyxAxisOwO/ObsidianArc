import { afterEach, describe, expect, it } from 'vitest';
import { createApp, nextTick, type App } from 'vue';
import ChatThinking from '../src/chat/ChatThinking.vue';

let app: App | undefined;

afterEach(() => {
  app?.unmount();
  app = undefined;
});

describe('completed reasoning', () => {
  it('stays expanded at handoff and can still be collapsed by the reader', async () => {
    const host = document.createElement('div');
    document.body.append(host);
    app = createApp(ChatThinking, { text: 'thinking', initialOpen: true });
    app.mount(host);

    const box = host.querySelector('.ai-thinking');
    const button = host.querySelector<HTMLButtonElement>('.ai-thinking-head');
    expect(box?.classList.contains('open')).toBe(true);
    expect(button?.getAttribute('aria-expanded')).toBe('true');

    button?.click();
    await nextTick();
    expect(box?.classList.contains('open')).toBe(false);
    expect(button?.getAttribute('aria-expanded')).toBe('false');
  });
});
