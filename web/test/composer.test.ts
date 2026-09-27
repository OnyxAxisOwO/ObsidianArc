import { afterEach, beforeEach, describe, expect, it } from 'vitest';
import { createApp, nextTick, type App } from 'vue';
import ChatComposer from '../src/chat/ChatComposer.vue';
import { draft, resetChat } from '../src/chat/useChat';

describe('ChatComposer layout and multiline behavior', () => {
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
    document.body.textContent = '';
    draft.value = '';
    resetChat();
  });

  it('renders textarea above the actions row so multiline prompts use full width', () => {
    app = createApp(ChatComposer);
    app.mount(host);

    const form = host.querySelector('form.ai-chat-composer');
    expect(form).not.toBeNull();

    // Textarea is placed above the controls row so it spans the entire card width
    // rather than being squeezed into a narrow strip next to the model chip.
    const textarea = form?.querySelector<HTMLTextAreaElement>(':scope > textarea.ai-chat-input');
    expect(textarea).not.toBeNull();

    // The actions row sits below the textarea.
    const row = form?.querySelector('.ai-chat-composer-row');
    expect(row).not.toBeNull();

    // Actions row contains the plus menu on the left and tools on the right.
    const plus = row?.querySelector('.ai-chat-plus');
    expect(plus).not.toBeNull();

    const tools = row?.querySelector('.ai-chat-composer-tools');
    expect(tools).not.toBeNull();

    // Model control and send button sit together in the tools container.
    expect(tools?.querySelector('.ai-model-control')).not.toBeNull();
    expect(tools?.querySelector('.ai-chat-send')).not.toBeNull();
  });

  it('resizes textarea when typing multiple lines without shifting buttons inline with text', async () => {
    app = createApp(ChatComposer);
    app.mount(host);

    const textarea = host.querySelector<HTMLTextAreaElement>('textarea.ai-chat-input')!;
    expect(textarea).not.toBeNull();

    // Mock scrollHeight for single vs multiline content
    Object.defineProperty(textarea, 'scrollHeight', {
      configurable: true,
      get: () => (draft.value.includes('\n') ? 72 : 28),
    });

    draft.value = 'line 1\nline 2\nline 3';
    await nextTick();
    await nextTick();

    expect(textarea.style.height).toBe('72px');

    draft.value = 'single line';
    await nextTick();
    await nextTick();

    expect(textarea.style.height).toBe('28px');
  });
});
