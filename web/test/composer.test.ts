import { afterEach, beforeEach, describe, expect, it } from 'vitest';
import { createApp, nextTick, type App } from 'vue';
import ChatComposer from '../src/chat/ChatComposer.vue';
import { flash, resetChat } from '../src/chat/useChat';
import { models, selectedID } from '../src/chat/useModels';
import { t } from '../src/composables/useI18n';

// A picture pasted into a model that cannot read it used to be swallowed by
// the paste handler with nothing said, because the refusal happened later.
describe('pasting a picture', () => {
  let app: App | null = null;
  let host: HTMLElement;

  beforeEach(() => {
    document.body.textContent = '';
    host = document.createElement('div');
    document.body.appendChild(host);
    models.value = [{ id: 'm', display_name: 'M', supports_images: false } as (typeof models.value)[number]];
    selectedID.value = 'm';
  });

  afterEach(() => {
    app?.unmount();
    app = null;
    resetChat();
  });

  it('says why, and leaves the paste to the browser, when the model cannot read pictures', async () => {
    app = createApp(ChatComposer);
    app.mount(host);

    const textarea = host.querySelector<HTMLTextAreaElement>('textarea.ai-chat-input')!;
    const paste = new Event('paste', { bubbles: true, cancelable: true });
    Object.defineProperty(paste, 'clipboardData', {
      value: { files: [new File(['png'], 'shot.png', { type: 'image/png' })] },
    });
    textarea.dispatchEvent(paste);
    await nextTick();

    expect(paste.defaultPrevented).toBe(false);
    expect(flash.value).toBe(t('imagesUnsupported'));
  });
});
