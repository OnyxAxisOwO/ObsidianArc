import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import { createApp, h, nextTick, type App } from 'vue';
import type { AttachmentRef, Message } from '../src/api/chat';
import { sendTurn } from '../src/api/chat';
import ChatMessage from '../src/chat/ChatMessage.vue';
import { busy, editingID, flash, resetChat } from '../src/chat/useChat';
import { models, selectedID } from '../src/chat/useModels';
import { t } from '../src/composables/useI18n';

vi.mock('@/api/chat', () => ({
  attachmentURL: (id: string) => `/api/attachments/${id}`,
  updateMessage: vi.fn(),
  listConversations: vi.fn(async () => ({ conversations: [] })),
  getConversation: vi.fn(async () => ({ conversation: undefined, messages: [] })),
  renameConversation: vi.fn(),
  deleteConversation: vi.fn(),
  deleteAllConversations: vi.fn(),
  archiveConversation: vi.fn(),
  uploadAttachment: vi.fn(),
  sendTurn: vi.fn(async () => {}),
}));

const USER_ID = '01ARZ3NDEKTSV4RRFFQ69G5FU1';
const LIVE = { id: '01ARZ3NDEKTSV4RRFFQ69G5FA1', mime: 'image/jpeg', width: 8, height: 8, size: 64 };
const GONE = { ...LIVE, id: '01ARZ3NDEKTSV4RRFFQ69G5FA2', discarded: true };

let app: App | null = null;
let host: HTMLElement;

function configureModel(): void {
  models.value = [{ id: 'm', display_name: 'M', supports_images: true } as (typeof models.value)[number]];
  selectedID.value = 'm';
}

function userMessage(content: string, attachments: AttachmentRef[]): Message {
  return { id: USER_ID, seq: 1, role: 'user', content, created_at: 0, attachments };
}

/** Mounts a user turn and opens its editor, as the Edit button does. */
async function mountEditing(message: Message): Promise<void> {
  app = createApp({ render: () => h(ChatMessage, { message }) });
  app.mount(host);
  const edit = [...host.querySelectorAll<HTMLButtonElement>('.ai-msg-actions button')]
    .find((button) => button.textContent?.trim() === t('edit'));
  edit?.click();
  await nextTick();
}

/** The editor's own draft: ChatMessage keeps it apart from the composer's. */
async function typeInEditor(value: string): Promise<void> {
  const textarea = host.querySelector<HTMLTextAreaElement>('.ai-msg-edit-input');
  if (!textarea) throw new Error('the editor did not open');
  textarea.value = value;
  textarea.dispatchEvent(new Event('input'));
  await nextTick();
}

function saveAndResend(): void {
  host.querySelector<HTMLButtonElement>('.ai-msg-editor-actions .primary')?.click();
}

describe('editing a question and sending it again', () => {
  beforeEach(() => {
    document.body.textContent = '';
    host = document.createElement('div');
    document.body.appendChild(host);
    configureModel();
    vi.mocked(sendTurn).mockClear();
  });

  afterEach(() => {
    app?.unmount();
    app = null;
    resetChat();
  });

  it('does not send an emptied question, which would only delete it', async () => {
    await mountEditing(userMessage('What is in this picture?', []));
    await typeInEditor('   ');

    saveAndResend();
    await nextTick();

    expect(sendTurn).not.toHaveBeenCalled();
    expect(editingID.value).toBe(USER_ID);
  });

  it('keeps the editor open, with a reason, when only pictures the server dropped remain', async () => {
    await mountEditing(userMessage('', [GONE]));
    await typeInEditor('');

    saveAndResend();
    await nextTick();

    expect(sendTurn).not.toHaveBeenCalled();
    expect(editingID.value).toBe(USER_ID);
    expect(flash.value).toBe(t('imageDiscarded'));
  });

  it('sends the pictures it still has, and says which ones it could not send', async () => {
    await mountEditing(userMessage('Describe it.', [LIVE, GONE]));
    await typeInEditor('Describe it again.');

    saveAndResend();
    await vi.waitFor(() => expect(sendTurn).toHaveBeenCalledTimes(1));

    const [request] = vi.mocked(sendTurn).mock.calls[0]!;
    expect(request).toMatchObject({
      content: 'Describe it again.',
      truncate_from_message_id: USER_ID,
      attachment_ids: [LIVE.id],
    });
    expect(flash.value).toBe(t('imageDiscarded'));
  });

  it('keeps the editor open while a turn is running, instead of dropping the edit', async () => {
    await mountEditing(userMessage('What is in this picture?', []));
    busy.value = true;
    await typeInEditor('Changed my mind.');

    saveAndResend();
    await nextTick();

    expect(sendTurn).not.toHaveBeenCalled();
    expect(editingID.value).toBe(USER_ID);
    expect(host.querySelector<HTMLTextAreaElement>('.ai-msg-edit-input')?.value).toBe('Changed my mind.');
    busy.value = false;
  });

  it('offers no Edit button on a question while a turn is running', async () => {
    busy.value = true;
    app = createApp({ render: () => h(ChatMessage, { message: userMessage('Hello', []) }) });
    app.mount(host);

    const labels = [...host.querySelectorAll<HTMLButtonElement>('.ai-msg-actions button')]
      .map((button) => button.textContent?.trim());
    expect(labels).not.toContain(t('edit'));
    busy.value = false;
  });

  it('offers Regenerate on an answer, and hides it while a turn is running', async () => {
    const answer: Message = { id: 'A1', seq: 2, role: 'assistant', content: 'An answer.', created_at: 0 };
    app = createApp({ render: () => h(ChatMessage, { message: answer }) });
    app.mount(host);
    const labels = () => [...host.querySelectorAll<HTMLButtonElement>('button')]
      .map((button) => button.textContent?.trim());

    // The positive control: a row that never rendered would also lack the
    // label, so the absence below proves nothing without this.
    expect(labels()).toContain(t('regenerate'));

    busy.value = true;
    await nextTick();
    expect(labels()).not.toContain(t('regenerate'));
    busy.value = false;
  });
});
