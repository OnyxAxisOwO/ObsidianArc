import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import { createApp, h, nextTick, type App } from 'vue';
import type { Message } from '../src/api/chat';
import ChatMessage from '../src/chat/ChatMessage.vue';
import { changeLanguage, t } from '../src/composables/useI18n';

vi.mock('@/api/chat', () => ({
  attachmentURL: (id: string) => `/api/attachments/${id}`,
  updateMessage: vi.fn(),
}));

const SERVER_SENTENCE = 'The provider is rate limiting this server. Try again shortly.';
const ID = '01ARZ3NDEKTSV4RRFFQ69G5FA9';

let app: App | null = null;
let host: HTMLElement;

beforeEach(async () => {
  await changeLanguage('zh');
  host = document.createElement('div');
  document.body.appendChild(host);
});

afterEach(async () => {
  app?.unmount();
  app = null;
  host.remove();
  await changeLanguage('en');
});

function storedFailure(errorCode: string): Message {
  return { id: ID, seq: 2, role: 'assistant', content: '', error: SERVER_SENTENCE, error_code: errorCode, created_at: 0 };
}

function bubbleText(): string {
  return host.querySelector('.ai-chat-error-text')?.textContent?.trim() ?? '';
}

describe('a stored failure bubble', () => {
  // Reopening a conversation draws the row, not the live stream, so the bubble
  // must translate from the stored code rather than show the English row.
  it('shows the reader\'s sentence for the stored code, and follows a language change', async () => {
    app = createApp({ render: () => h(ChatMessage, { message: storedFailure('provider_rate_limited') }) });
    app.mount(host);
    await nextTick();

    expect(bubbleText()).toBe(t('errProviderRateLimited'));
    expect(bubbleText()).not.toBe(SERVER_SENTENCE);

    await changeLanguage('en');
    await nextTick();
    expect(bubbleText()).toBe(t('errProviderRateLimited'));
  });

  it('keeps the stored sentence for a row written before codes were stored', async () => {
    app = createApp({ render: () => h(ChatMessage, { message: storedFailure('') }) });
    app.mount(host);
    await nextTick();

    expect(bubbleText()).toBe(SERVER_SENTENCE);
  });
});
