// When a turn that failed hands its text back to the composer. The server
// commits the user's message before it sends `start`, so only a refusal it
// answered with a status, before the stream opened, means the turn never
// happened. Anything after that point has already saved the message, and
// giving the text back would send it a second time.

import { beforeEach, describe, expect, it, vi } from 'vitest';
import { sendTurn } from '../src/api/chat';
import { ApiError } from '../src/api/client';
import { draft, resetChat, runTurn, stop } from '../src/chat/useChat';
import { models, selectedID } from '../src/chat/useModels';

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
  sendTurn: vi.fn(),
}));

function configureModel(): void {
  models.value = [{ id: 'm', display_name: 'M' } as (typeof models.value)[number]];
  selectedID.value = 'm';
}

beforeEach(() => {
  resetChat();
  configureModel();
  draft.value = '';
  vi.mocked(sendTurn).mockReset();
});

describe('a turn that fails', () => {
  it('hands the text back when the server refused the request before the stream opened', async () => {
    vi.mocked(sendTurn).mockRejectedValueOnce(new ApiError(409, 'conflict', 'The conversation is busy.'));

    await expect(runTurn({ content: 'hello' })).resolves.toBe('unsent');
    expect(draft.value).toBe('hello');
  });

  it('hands the text back when the server reports an error before the start frame', async () => {
    // The server sends this when its opening transaction rolled back: it is
    // an event on the stream, not a thrown error, so it must not be read as a
    // turn that happened.
    vi.mocked(sendTurn).mockImplementationOnce(async (_request, handlers) => {
      handlers.onError?.({ code: 'internal', message: 'x' });
    });

    await expect(runTurn({ content: 'hello' })).resolves.toBe('unsent');
    expect(draft.value).toBe('hello');
  });

  it('does not hand the text back when the server errors after the start frame', async () => {
    vi.mocked(sendTurn).mockImplementationOnce(async (_request, handlers) => {
      handlers.onStart?.({ conversation_id: 'c1', title: 't', model_id: 'm', model_name: 'M' });
      handlers.onError?.({ code: 'internal', message: 'x' });
    });

    await expect(runTurn({ content: 'hello' })).resolves.toBe('done');
    expect(draft.value).toBe('');
  });

  it('does not hand the text back when the connection drops after the server took the message', async () => {
    vi.mocked(sendTurn).mockRejectedValueOnce(new ApiError(0, 'network', 'Could not reach the server'));

    await expect(runTurn({ content: 'hello' })).resolves.toBe('done');
    expect(draft.value).toBe('');
  });

  it('does not hand the text back when the reader stops before the start frame', async () => {
    vi.mocked(sendTurn).mockImplementationOnce(
      (_request, _handlers, signal) =>
        new Promise<void>((_resolve, reject) => {
          signal.addEventListener('abort', () => reject(new DOMException('aborted', 'AbortError')));
        }),
    );

    const running = runTurn({ content: 'hello' });
    stop();

    await expect(running).resolves.toBe('done');
    expect(draft.value).toBe('');
  });
});
