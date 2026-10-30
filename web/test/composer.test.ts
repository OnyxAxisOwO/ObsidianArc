import { afterEach, beforeEach, describe, expect, it } from 'vitest';
import { createApp, nextTick, type App } from 'vue';
import ChatComposer from '../src/chat/ChatComposer.vue';
import { activeID, draft, flash, openingID, resetChat, webSearchOn } from '../src/chat/useChat';
import { models, selectedID } from '../src/chat/useModels';
import { t } from '../src/composables/useI18n';
import type { SiteInfo } from '../src/api/auth';
import { site } from '../src/stores/session';

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
    draft.value = '';
    resetChat();
    models.value = [];
    selectedID.value = '';
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

  it('does not offer to send while the next conversation is being read', () => {
    models.value = [{ id: 'm', display_name: 'M' } as (typeof models.value)[number]];
    selectedID.value = 'm';
    activeID.value = 'B';
    openingID.value = 'B';
    app = createApp(ChatComposer);
    app.mount(host);

    const send = host.querySelector<HTMLButtonElement>('.ai-chat-send');
    expect(send).not.toBeNull();
    expect(send!.disabled).toBe(true);
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
