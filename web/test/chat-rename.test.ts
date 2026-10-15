import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import { renameConversation, type Conversation } from '../src/api/chat';
import { active, activeID, conversations, flash, rename } from '../src/chat/useChat';
import { t } from '../src/composables/useI18n';

vi.mock('@/api/chat', () => ({
  attachmentURL: (id: string) => `/api/attachments/${id}`,
  archiveConversation: vi.fn(),
  deleteAllConversations: vi.fn(),
  deleteConversation: vi.fn(),
  getConversation: vi.fn(async () => ({ conversation: undefined, messages: [] })),
  listConversations: vi.fn(async () => ({ conversations: [] })),
  renameConversation: vi.fn(),
  sendTurn: vi.fn(),
  updateMessage: vi.fn(),
  uploadAttachment: vi.fn(),
}));

function conversation(id: string, title: string): Conversation {
  return {
    id,
    title,
    model_id: 'm',
    pinned: false,
    message_count: 0,
    created_at: 0,
    updated_at: 0,
  } as Conversation;
}

beforeEach(() => {
  vi.spyOn(console, 'warn').mockImplementation(() => {});
  flash.value = '';
  activeID.value = 'c1';
  conversations.value = [conversation('c1', 'Old')];
});

afterEach(() => {
  vi.restoreAllMocks();
  conversations.value = [];
  activeID.value = '';
  flash.value = '';
});

describe('renaming a conversation', () => {
  it('renames the rail and header copy when the reader renames it from a project row', async () => {
    // The project list fetches its own copies of its conversations, so the row
    // it renames is not the object the rail and the header read.
    const projectRow = conversation('c1', 'Old');
    vi.spyOn(window, 'prompt').mockReturnValue('New');
    vi.mocked(renameConversation).mockResolvedValue({ conversation: conversation('c1', 'New') });

    await rename(projectRow);

    expect(projectRow.title).toBe('New');
    expect(conversations.value[0]?.title).toBe('New');
    expect(active.value?.title).toBe('New');
  });

  it('shows the translated generic failure, not the raw English of a network error', async () => {
    vi.spyOn(window, 'prompt').mockReturnValue('New');
    vi.mocked(renameConversation).mockRejectedValue(new TypeError('Failed to fetch'));

    await rename(conversation('c1', 'Old'));

    expect(flash.value).toBe(t('failed'));
    expect(flash.value).not.toContain('TypeError');
    expect(conversations.value[0]?.title).toBe('Old');
  });
});
