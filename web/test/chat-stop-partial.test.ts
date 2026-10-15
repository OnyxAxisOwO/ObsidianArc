import { beforeEach, describe, expect, it, vi } from 'vitest';

import type { Message, TurnHandlers } from '../src/api/chat';
import { getConversation, sendTurn } from '../src/api/chat';
import { activeID, busy, flushDeltas, messages, pending, resetChat, runTurn, stop } from '../src/chat/useChat';
import { models, selectedID } from '../src/chat/useModels';

// Stop closes the stream, and the server saves the streamed answer after that
// connection has gone. The client used to reload right away, before the save,
// and then clear the streamed text: the answer vanished until the conversation
// was reopened. These cases hold the composer to the saved row.

const turn: { handlers: TurnHandlers | null } = { handlers: null };

vi.mock('@/api/chat', () => ({
  attachmentURL: (id: string) => `/api/attachments/${id}`,
  updateMessage: vi.fn(),
  listConversations: vi.fn(async () => ({ conversations: [] })),
  getConversation: vi.fn(),
  renameConversation: vi.fn(),
  deleteConversation: vi.fn(),
  deleteAllConversations: vi.fn(),
  archiveConversation: vi.fn(),
  uploadAttachment: vi.fn(),
  sendTurn: vi.fn(),
}));

const userRow: Message = { id: 'u1', seq: 1, role: 'user', content: 'hi', created_at: 0 };
const savedRow: Message = { id: 'a1', seq: 2, role: 'assistant', content: 'tok01', created_at: 0 };

function conversation(...rows: Message[]): Awaited<ReturnType<typeof getConversation>> {
  return {
    conversation: { id: 'A', title: 'A', model_id: 'm', pinned: false, message_count: rows.length, created_at: 0, updated_at: 0 },
    messages: rows,
  };
}

function configureModel(): void {
  models.value = [{ id: 'm', display_name: 'M' } as (typeof models.value)[number]];
  selectedID.value = 'm';
}

// The turn stays open until the reader stops it, as a real stream does, and
// then rejects the way fetch rejects an aborted request.
function holdTurn(): void {
  vi.mocked(sendTurn).mockImplementation((_request, handlers, signal) => {
    turn.handlers = handlers;
    return new Promise<void>((_resolve, reject) => {
      signal.addEventListener('abort', () => reject(new DOMException('aborted', 'AbortError')));
    });
  });
}

function start(): void {
  turn.handlers?.onStart?.({ conversation_id: 'A', title: 'A', model_id: 'm', model_name: 'M' });
}

describe('stopping a turn', () => {
  beforeEach(() => {
    resetChat();
    configureModel();
    turn.handlers = null;
    vi.mocked(getConversation).mockReset();
    vi.mocked(getConversation).mockImplementation(async () => conversation(userRow));
    holdTurn();
  });

  it('keeps the streamed answer on screen until the server has saved it', async () => {
    // The streamed text the block held at each read. Checking only the final
    // state would pass even if the block were cleared before the first read.
    const streamedAtRead: Array<string | undefined> = [];
    const read = (rows: Message[]) => async () => {
      streamedAtRead.push(pending.value?.answer);
      return conversation(...rows);
    };
    vi.mocked(getConversation)
      .mockImplementationOnce(read([userRow]))
      .mockImplementation(read([userRow, savedRow]));

    const running = runTurn({ content: 'hi' });
    start();
    turn.handlers?.onDelta?.('tok01');
    flushDeltas();
    stop();
    await running;

    expect(vi.mocked(getConversation).mock.calls.length).toBeGreaterThanOrEqual(2);
    expect(streamedAtRead.length).toBeGreaterThanOrEqual(2);
    for (const answer of streamedAtRead) expect(answer).toBe('tok01');
    expect(messages.value[messages.value.length - 1]).toMatchObject({ role: 'assistant', content: 'tok01' });
    expect(busy.value).toBe(false);
    expect(pending.value).toBeNull();
  });

  it('reads once and releases the composer when the reader stops before any text arrives', async () => {
    const running = runTurn({ content: 'hi' });
    start();
    stop();
    await running;

    expect(vi.mocked(getConversation)).toHaveBeenCalledTimes(1);
    expect(busy.value).toBe(false);
    expect(pending.value).toBeNull();
  });

  it('releases the composer after the schedule when the saved row never arrives', async () => {
    vi.useFakeTimers();
    try {
      const running = runTurn({ content: 'hi' });
      start();
      turn.handlers?.onDelta?.('tok01');
      flushDeltas();
      stop();
      await vi.advanceTimersByTimeAsync(5000);
      await running;

      expect(vi.mocked(getConversation)).toHaveBeenCalledTimes(5);
      expect(activeID.value).toBe('A');
      expect(busy.value).toBe(false);
      expect(pending.value).toBeNull();
    } finally {
      vi.useRealTimers();
    }
  });
});
