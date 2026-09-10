import { beforeEach, describe, expect, it, vi } from 'vitest';

import type { TurnHandlers } from '../src/api/chat';

// Waiting for an answer used to mean waiting to read anything else: both
// `openConversation` and `startNewConversation` returned early while a turn
// was running, so the rail was inert until the model finished. Reading is not
// blocked by writing any more, and what that opens up is a class of bug the
// old guard made impossible — a turn finishing into a transcript the reader
// has already left. These are the assertions that the streamed block and the
// rows it becomes both stay with the conversation they belong to.

const turn: { handlers: TurnHandlers | null; finish: (() => void) | null } = {
  handlers: null,
  finish: null,
};

vi.mock('@/api/chat', () => ({
  listConversations: vi.fn(async () => ({ conversations: [] })),
  getConversation: vi.fn(async (id: string) => ({
    conversation: { id, title: id, model_id: 'm', pinned: false, message_count: 1, created_at: 0, updated_at: 0 },
    messages: [{ id: `${id}-row`, seq: 1, role: 'assistant', content: id, created_at: 0 }],
  })),
  renameConversation: vi.fn(),
  deleteConversation: vi.fn(),
  deleteAllConversations: vi.fn(),
  uploadAttachment: vi.fn(),
  sendTurn: vi.fn((_request: unknown, handlers: TurnHandlers) => {
    turn.handlers = handlers;
    return new Promise<void>((resolve) => { turn.finish = resolve; });
  }),
}));

const { activeID, busy, messages, openConversation, pendingID, resetChat, runTurn, showPending } =
  await import('../src/chat/useChat');
const { models, selectedID } = await import('../src/chat/useModels');

/** Enough of a model for `status.configured`; nothing here reads the rest. */
function configureModel(): void {
  models.value = [{ id: 'm', display_name: 'M' } as (typeof models.value)[number]];
  selectedID.value = 'm';
}

function start(): void {
  turn.handlers?.onStart?.({ conversation_id: 'A', title: 'A', model_id: 'm', model_name: 'M' });
}

describe('switching conversations while a turn runs', () => {
  beforeEach(() => {
    resetChat();
    configureModel();
    turn.handlers = null;
    turn.finish = null;
  });

  it('opens another conversation without waiting, and keeps the stream with its own', async () => {
    const running = runTurn({ content: 'hi' });
    start();

    expect(activeID.value).toBe('A');
    expect(pendingID.value).toBe('A');
    expect(showPending.value).toBe(true);

    // The whole point: this used to return without doing anything.
    await openConversation('B');
    expect(activeID.value).toBe('B');
    expect(messages.value[0]?.id).toBe('B-row');
    expect(busy.value).toBe(true);
    // The answer is still being written, but not here.
    expect(showPending.value).toBe(false);

    // Back where it is being written, the same turn is on screen again.
    await openConversation('A');
    expect(showPending.value).toBe(true);

    turn.finish?.();
    await running;
    expect(messages.value[0]?.id).toBe('A-row');
    expect(busy.value).toBe(false);
    expect(pendingID.value).toBe('');
  });

  it('does not paint a finished turn into the conversation the reader moved to', async () => {
    const running = runTurn({ content: 'hi' });
    start();
    await openConversation('B');

    turn.finish?.();
    await running;

    // B's rows, not A's: the turn's transcript is not the reader's to
    // overwrite once they have left it.
    expect(activeID.value).toBe('B');
    expect(messages.value.every((message) => message.id === 'B-row')).toBe(true);
  });
});
