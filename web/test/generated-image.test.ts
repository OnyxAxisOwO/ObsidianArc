import { describe, it, expect, beforeEach, afterEach } from 'vitest';
import { createApp, h, nextTick, type App } from 'vue';
import ChatMessage from '../src/chat/ChatMessage.vue';
import { changeLanguage, t } from '../src/composables/useI18n';
import type { Message } from '../src/api/chat';

let app: App | null = null;
let host: HTMLElement;

beforeEach(() => {
  document.body.textContent = '';
  host = document.createElement('div');
  document.body.appendChild(host);
});

afterEach(async () => {
  app?.unmount();
  app = null;
  // The dictionary is module state: a test that left Chinese loaded would
  // change what the next one reads.
  await changeLanguage('en');
});

/** Mounts one assistant turn carrying a single generated picture. */
function mountGeneratedImage(content: string): HTMLImageElement {
  app?.unmount();
  app = null;
  host.textContent = '';

  const message: Message = {
    id: '01ARZ3NDEKTSV4RRFFQ69G5FAV',
    seq: 1,
    role: 'assistant',
    content,
    created_at: Date.now(),
    attachments: [
      { id: '01ARZ3NDEKTSV4RRFFQ69G5FAA', mime: 'image/png', width: 512, height: 512, size: 2048 },
    ],
  };
  app = createApp({ render: () => h(ChatMessage, { message }) });
  app.mount(host);

  const image = host.querySelector<HTMLImageElement>('img.ai-chat-generated-img');
  if (!image) throw new Error('the generated image did not render');
  return image;
}

// `alt` is what a screen reader announces in place of the picture, so a
// generated image with no caption still needs a name — and it has to be in the
// reader's language. It was a hardcoded English string in an interface that
// ships in Chinese.
describe('the alt text of a generated image', () => {
  it('falls back to a translated name when the model wrote no caption', async () => {
    await changeLanguage('en');
    expect(mountGeneratedImage('').alt).toBe(t('generatedImage'));

    await changeLanguage('zh');
    await nextTick();
    const alt = mountGeneratedImage('').alt;
    expect(alt).toBe(t('generatedImage'));
    expect(alt).not.toBe('Generated image');
  });

  it('keeps the caption the model wrote', async () => {
    await changeLanguage('en');
    expect(mountGeneratedImage('a red fox in snow').alt).toBe('a red fox in snow');
  });
});

// A picture that opens a larger view has to be reachable without a pointer:
// the picture itself was a bare click handler, with no focus and no role.
describe('zooming a generated image from the keyboard', () => {
  it('is a focusable button with a translated name', async () => {
    await changeLanguage('en');
    const image = mountGeneratedImage('a red fox in snow');
    expect(image.getAttribute('role')).toBe('button');
    expect(image.getAttribute('tabindex')).toBe('0');
    expect(image.getAttribute('aria-label')).toBe(t('zoomImage'));
  });

  it.each(['Enter', ' '])('opens the lightbox on the %j key', async (key) => {
    await changeLanguage('en');
    const image = mountGeneratedImage('a red fox in snow');

    image.dispatchEvent(new KeyboardEvent('keydown', { key, bubbles: true }));
    await nextTick();
    expect(document.querySelector('.oa-lightbox-img')).not.toBeNull();
  });
});
