import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import { ApiError } from '../src/api/client';
import type { AttachmentRef, TurnHandlers } from '../src/api/chat';
import { sendTurn, uploadAttachment, archiveConversation as archiveConversationApi } from '../src/api/chat';
import {
  addImages, addTextFiles, archiveConversation, attachments, busy, conversations, draft, flash,
  messages, removeConversation, resetChat, runTurn, submit,
} from '../src/chat/useChat';
import { models, selectedID } from '../src/chat/useModels';
import { t } from '../src/composables/useI18n';

// What the composer does with the drafts it is given. The network and the
// canvas are stubbed; everything between them is the store's own code.

vi.mock('@/api/chat', () => ({
  archiveConversation: vi.fn(async () => ({})),
  deleteAllConversations: vi.fn(),
  deleteConversation: vi.fn(async () => ({})),
  getConversation: vi.fn(async () => ({ conversation: undefined, messages: [] })),
  listConversations: vi.fn(async () => ({ conversations: [] })),
  renameConversation: vi.fn(),
  sendTurn: vi.fn(),
  uploadAttachment: vi.fn(),
}));

vi.mock('../src/chat/image', () => ({
  ImageError: class extends Error {},
  prepareImage: vi.fn(async (file: File) => ({
    mime: 'image/jpeg', data: 'AAAA', width: 1, height: 1, bytes: 4, previewURL: `blob:${file.name}`,
  })),
}));

const turn: { handlers: TurnHandlers | null; finish: ((error?: unknown) => void) | null } = {
  handlers: null,
  finish: null,
};

function configureModel(vision: boolean): void {
  models.value = [{ id: 'm', display_name: 'M', supports_images: vision } as (typeof models.value)[number]];
  selectedID.value = 'm';
}

function ref(id: string): AttachmentRef {
  return { id, mime: 'image/jpeg', width: 1, height: 1, size: 4 };
}

/** A text file whose contents are known, since jsdom's File.text may be absent. */
function textFile(name: string, content: string): File {
  const file = new File([content], name, { type: 'text/plain' });
  Object.defineProperty(file, 'text', { value: async () => content });
  return file;
}

function imageFile(name: string): File {
  return new File(['png'], name, { type: 'image/png' });
}

describe('sending a turn the server does not take', () => {
  beforeEach(() => {
    resetChat();
    configureModel(true);
    turn.handlers = null;
    turn.finish = null;
    vi.mocked(sendTurn).mockReset();
    Object.assign(URL, { createObjectURL: vi.fn(() => 'blob:x'), revokeObjectURL: vi.fn() });
  });

  afterEach(() => {
    resetChat();
  });

  it('puts the typed text back when the server refuses the request before it starts', async () => {
    vi.mocked(sendTurn).mockRejectedValueOnce(new ApiError(402, 'quota_exhausted', 'Quota exhausted', {}));

    draft.value = 'A long question I do not want to retype.';
    await submit();

    expect(draft.value).toBe('A long question I do not want to retype.');
    expect(busy.value).toBe(false);
    expect(messages.value.some((message) => message.id.startsWith('local-'))).toBe(false);
  });

  it('keeps the staged pictures for the next try when the server refuses the turn', async () => {
    vi.mocked(uploadAttachment).mockResolvedValueOnce({ attachment: ref('a1') });
    await addImages([imageFile('chart.png')]);
    vi.mocked(sendTurn).mockRejectedValueOnce(new ApiError(429, 'too_many_in_flight', 'Too many', {}));

    draft.value = 'Explain the chart.';
    await submit();

    expect(attachments.value.map((entry) => entry.ref.id)).toEqual(['a1']);
    expect(draft.value).toBe('Explain the chart.');
  });

  it('does not hand the text back once the server has started the turn', async () => {
    vi.mocked(sendTurn).mockImplementationOnce((_request, handlers) => {
      turn.handlers = handlers;
      return new Promise<void>((_resolve, reject) => { turn.finish = reject; });
    });

    draft.value = 'Saved on the server already.';
    const running = submit();
    turn.handlers?.onStart?.({ conversation_id: 'C', title: 'C', model_id: 'm', model_name: 'M' });
    turn.finish?.(new ApiError(500, 'internal', 'Boom', {}));
    await running;

    expect(draft.value).toBe('');
  });

  it('sends the pictures of an upload that finishes mid-turn on the next message, not this one', async () => {
    vi.mocked(uploadAttachment).mockResolvedValueOnce({ attachment: ref('a1') });
    await addImages([imageFile('first.png')]);

    let finishLate: ((value: { attachment: AttachmentRef }) => void) | undefined;
    vi.mocked(uploadAttachment).mockImplementationOnce(() => new Promise((resolve) => { finishLate = resolve; }));
    const uploading = addImages([imageFile('second.png')]);

    vi.mocked(sendTurn).mockImplementationOnce((_request, handlers) => {
      turn.handlers = handlers;
      return new Promise<void>((resolve) => { turn.finish = () => resolve(); });
    });
    draft.value = 'Compare them.';
    const running = submit();
    await vi.waitFor(() => expect(finishLate).toBeTypeOf('function'));

    finishLate!({ attachment: ref('a2') });
    await uploading;
    turn.handlers?.onStart?.({ conversation_id: 'C', title: 'C', model_id: 'm', model_name: 'M' });
    turn.finish?.();
    await running;

    expect(attachments.value.map((entry) => entry.ref.id)).toEqual(['a2']);
  });

  it('shows the message it sends with the pictures it carries, not whatever is staged', async () => {
    vi.mocked(uploadAttachment).mockResolvedValueOnce({ attachment: ref('a1') });
    await addImages([imageFile('chart.png')]);
    vi.mocked(sendTurn).mockImplementationOnce((_request, handlers) => {
      turn.handlers = handlers;
      return new Promise<void>((resolve) => { turn.finish = () => resolve(); });
    });

    // A picture is still staged for the next message, while this turn carries
    // another one, as a resent message does. The row must show what was sent.
    const running = runTurn({ content: 'What is this?', attachmentIDs: ['b2'], sentAttachments: [ref('b2')] });

    const optimistic = messages.value.find((message) => message.id.startsWith('local-'));
    expect(optimistic?.attachments).toEqual([ref('b2')]);
    turn.handlers?.onStart?.({ conversation_id: 'C', title: 'C', model_id: 'm', model_name: 'M' });
    turn.finish?.();
    await running;
  });
});

describe('the text of attached files', () => {
  beforeEach(() => {
    resetChat();
    configureModel(true);
    Object.assign(URL, { createObjectURL: vi.fn(() => 'blob:x'), revokeObjectURL: vi.fn() });
  });

  afterEach(() => {
    resetChat();
  });

  it('is cut to what the message has room for, since the server refuses the whole draft', async () => {
    draft.value = 'x'.repeat(30000);
    await addTextFiles([textFile('notes.txt', 'y'.repeat(24000))]);

    expect(draft.value.length).toBeLessThanOrEqual(32000);
    expect(draft.value).toContain('(truncated)');
    expect(flash.value).toBe(t('fileTruncated', { name: 'notes.txt' }));
  });

  it('uses a fence longer than any line of backticks in the file it wraps', async () => {
    const source = 'Intro\n```\ncode\n```\nOutro';
    await addTextFiles([textFile('README.md', source)]);

    expect(draft.value).toContain('````markdown\n' + source + '\n````');
  });

  it('gives a file named after an Object property no language tag', async () => {
    // `known` is a plain object, so `constructor` would otherwise resolve to
    // Object's own function and be written into the fence as its language.
    await addTextFiles([textFile('notes.constructor', 'plain')]);

    expect(draft.value).toContain('```\nplain\n```');
  });
});

describe('the number of pictures in a message', () => {
  beforeEach(() => {
    resetChat();
    configureModel(true);
    vi.mocked(uploadAttachment).mockReset();
    Object.assign(URL, { createObjectURL: vi.fn(() => 'blob:x'), revokeObjectURL: vi.fn() });
  });

  afterEach(() => {
    resetChat();
  });

  it('never exceeds six when two picks arrive together', async () => {
    let counter = 0;
    vi.mocked(uploadAttachment).mockImplementation(async () => ({ attachment: ref(`p${counter++}`) }));

    await Promise.all([
      addImages(Array.from({ length: 4 }, (_, index) => imageFile(`a${index}.png`))),
      addImages(Array.from({ length: 4 }, (_, index) => imageFile(`b${index}.png`))),
    ]);

    expect(attachments.value.length).toBe(6);
  });

  it('says so, rather than refusing silently, when the model cannot read pictures', async () => {
    configureModel(false);
    await addImages([imageFile('photo.png')]);

    expect(attachments.value).toHaveLength(0);
    expect(flash.value).toBe(t('imagesUnsupported'));
    expect(uploadAttachment).not.toHaveBeenCalled();
  });
});

describe('a conversation the server did not change', () => {
  beforeEach(() => {
    resetChat();
    configureModel(true);
    conversations.value = [{
      id: 'C1', title: 'Kept', model_id: 'm', pinned: false, message_count: 0, created_at: 0, updated_at: 0,
    }];
  });

  afterEach(() => {
    resetChat();
  });

  it('reports failure from archive and delete, so the caller keeps the row', async () => {
    vi.mocked(archiveConversationApi).mockRejectedValueOnce(new ApiError(500, 'boom', 'Boom', {}));
    const row = conversations.value[0]!;

    expect(await archiveConversation(row)).toBe(false);
    expect(conversations.value.map((entry) => entry.id)).toEqual(['C1']);

    const { deleteConversation } = await import('../src/api/chat');
    vi.mocked(deleteConversation).mockRejectedValueOnce(new ApiError(500, 'boom', 'Boom', {}));
    expect(await removeConversation(row)).toBe(false);
    expect(conversations.value.map((entry) => entry.id)).toEqual(['C1']);
  });
});
