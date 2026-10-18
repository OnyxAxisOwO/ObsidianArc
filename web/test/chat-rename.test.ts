import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import { renameConversation, type Conversation } from '../src/api/chat';
import { active, activeID, conversations, flash, rename, renamingID } from '../src/chat/useChat';
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
  vi.mocked(renameConversation).mockReset();
  renamingID.value = '';
  conversations.value = [];
  activeID.value = '';
  flash.value = '';
});

describe('renaming a conversation', () => {
  it('renames the rail and header copy when the reader renames it from a project row', async () => {
    // The project list fetches its own copies of its conversations, so the row
    // it renames is not the object the rail and the header read.
    const projectRow = conversation('c1', 'Old');
    vi.mocked(renameConversation).mockResolvedValue({ conversation: conversation('c1', 'New') });

    renamingID.value = 'c1';
    await rename(projectRow, '  New ');

    expect(projectRow.title).toBe('New');
    expect(conversations.value[0]?.title).toBe('New');
    expect(active.value?.title).toBe('New');
  });

  it('shows the translated generic failure, not the raw English of a network error', async () => {
    vi.mocked(renameConversation).mockRejectedValue(new TypeError('Failed to fetch'));

    renamingID.value = 'c1';
    await rename(conversation('c1', 'Old'), 'New');

    expect(flash.value).toBe(t('failed'));
    expect(flash.value).not.toContain('TypeError');
    expect(conversations.value[0]?.title).toBe('Old');
  });

  it('sends nothing for an unchanged or emptied title, and ends the edit either way', async () => {
    for (const typed of ['Old', '   ']) {
      renamingID.value = 'c1';
      await rename(conversation('c1', 'Old'), typed);
      expect(renamingID.value).toBe('');
    }
    expect(renameConversation).not.toHaveBeenCalled();
  });

  it('submits once when the field is submitted and then blurs as it goes', async () => {
    vi.mocked(renameConversation).mockResolvedValue({ conversation: conversation('c1', 'New') });
    const row = conversation('c1', 'Old');
    renamingID.value = 'c1';
    await Promise.all([rename(row, 'New'), rename(row, 'New')]);
    expect(renameConversation).toHaveBeenCalledTimes(1);
    expect(renameConversation).toHaveBeenCalledWith('c1', 'New');
  });

  it('shows the new title before the server answers', async () => {
    let answer: (value: { conversation: Conversation }) => void = () => {};
    vi.mocked(renameConversation).mockImplementation(() => new Promise((resolve) => { answer = resolve; }));

    renamingID.value = 'c1';
    const pending = rename(conversations.value[0]!, 'New');
    expect(conversations.value[0]?.title).toBe('New');

    answer({ conversation: conversation('c1', 'New') });
    await pending;
    expect(conversations.value[0]?.title).toBe('New');
  });

  it('puts the old title back on both copies when the server refuses', async () => {
    const projectRow = conversation('c1', 'Old');
    let refuse: (reason: unknown) => void = () => {};
    vi.mocked(renameConversation).mockImplementation(() => new Promise((_, reject) => { refuse = reject; }));

    renamingID.value = 'c1';
    const pending = rename(projectRow, 'New');
    expect(projectRow.title).toBe('New');
    expect(conversations.value[0]?.title).toBe('New');

    refuse(new TypeError('Failed to fetch'));
    await pending;
    expect(projectRow.title).toBe('Old');
    expect(conversations.value[0]?.title).toBe('Old');
    expect(flash.value).toBe(t('failed'));
  });

  it('leaves a later rename alone when an earlier one is refused', async () => {
    const refusals: Array<(reason: unknown) => void> = [];
    vi.mocked(renameConversation).mockImplementation(() => new Promise((_, reject) => { refusals.push(reject); }));
    const row = conversations.value[0]!;

    renamingID.value = 'c1';
    const first = rename(row, 'First');
    renamingID.value = 'c1';
    void rename(row, 'Second');

    refusals[0]!(new TypeError('Failed to fetch'));
    await first;
    expect(row.title).toBe('Second');
  });
});
