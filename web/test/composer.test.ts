import { afterEach, beforeEach, describe, expect, it } from 'vitest';
import { createApp, nextTick, type App } from 'vue';
import ChatComposer from '../src/chat/ChatComposer.vue';
import { activeID, flash, openingID, resetChat, webSearchOn } from '../src/chat/useChat';
import { models, selectedID } from '../src/chat/useModels';
import { t } from '../src/composables/useI18n';
import type { SiteInfo } from '../src/api/auth';
import { site } from '../src/stores/session';

// Sending while a conversation is still being read would write into the one
// on screen, which is not the one the reader selected.
describe('sending while a conversation is opening', () => {
  let app: App | null = null;
  let host: HTMLElement;

  beforeEach(() => {
    document.body.textContent = '';
    host = document.createElement('div');
    document.body.appendChild(host);
    models.value = [{ id: 'm', display_name: 'M' } as (typeof models.value)[number]];
    selectedID.value = 'm';
  });

  afterEach(() => {
    app?.unmount();
    app = null;
    resetChat();
    models.value = [];
    selectedID.value = '';
  });

  it('does not offer to send while the next conversation is being read', () => {
    activeID.value = 'B';
    openingID.value = 'B';
    app = createApp(ChatComposer);
    app.mount(host);

    const send = host.querySelector<HTMLButtonElement>('.ai-chat-send');
    expect(send).not.toBeNull();
    expect(send!.disabled).toBe(true);
  });
});

// The switch is drawn from the instance's public setting, not from a local
// preference: an operator who turns web search off takes the control away
// from every composer on the next page load.
describe('the web search switch', () => {
  let app: App | null = null;
  let host: HTMLElement;

  beforeEach(() => {
    document.body.textContent = '';
    host = document.createElement('div');
    document.body.appendChild(host);
    models.value = [{ id: 'm', display_name: 'M' } as (typeof models.value)[number]];
    selectedID.value = 'm';
  });

  afterEach(() => {
    app?.unmount();
    app = null;
    site.value = null;
    resetChat();
    models.value = [];
    selectedID.value = '';
  });

  it('is hidden while the instance does not offer it, and toggles when it does', async () => {
    site.value = { web_search: false } as unknown as SiteInfo;
    app = createApp(ChatComposer);
    app.mount(host);
    expect(host.querySelector('.ai-chat-websearch')).toBeNull();

    site.value = { web_search: true } as unknown as SiteInfo;
    await nextTick();
    const button = host.querySelector<HTMLButtonElement>('.ai-chat-websearch');
    expect(button).not.toBeNull();
    expect(button?.getAttribute('aria-pressed')).toBe('false');

    button?.click();
    await nextTick();
    expect(webSearchOn.value).toBe(true);
    expect(button?.getAttribute('aria-pressed')).toBe('true');
    expect(button?.classList.contains('on')).toBe(true);
  });
});

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
    models.value = [];
    selectedID.value = '';
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
