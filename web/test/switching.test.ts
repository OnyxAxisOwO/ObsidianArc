import { beforeEach, describe, expect, it, vi } from 'vitest';

import type { TurnHandlers } from '../src/api/chat';
import { getConversation } from '../src/api/chat';
import { t } from '../src/composables/useI18n';

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
  archiveConversation: vi.fn(async () => {}),
  deleteConversation: vi.fn(async () => {}),
  deleteAllConversations: vi.fn(),
  uploadAttachment: vi.fn(),
  sendTurn: vi.fn((_request: unknown, handlers: TurnHandlers) => {
    turn.handlers = handlers;
    return new Promise<void>((resolve) => { turn.finish = resolve; });
  }),
}));

const { activeID, archiveConversation, busy, conversations, flash, flushDeltas, justSentID, messages,
  openConversation, pending, pendingID, recentReasoningID, removeConversation, resetChat, runTurn, showPending,
  stoppable } =
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
    vi.mocked(getConversation).mockImplementation(async (id: string) => ({
      conversation: { id, title: id, model_id: 'm', pinned: false, message_count: 1, created_at: 0, updated_at: 0 },
      messages: [{ id: `${id}-row`, seq: 1, role: 'assistant', content: id, created_at: 0 }],
    }));
    turn.handlers = null;
    turn.finish = null;
  });

  it('keeps the streamed answer and reasoning visible until the saved row arrives', async () => {
    let finishReload: ((value: Awaited<ReturnType<typeof getConversation>>) => void) | undefined;
    vi.mocked(getConversation).mockImplementationOnce(() => new Promise((resolve) => { finishReload = resolve; }));

    const running = runTurn({ content: 'hi' });
    start();
    turn.handlers?.onDelta?.('answer');
    turn.handlers?.onReasoning?.('thinking');
    flushDeltas();
    turn.handlers?.onDone?.({ message_id: 'saved', stopped: false, streamed: true });
    turn.finish?.();
    await vi.waitFor(() => expect(finishReload).toBeTypeOf('function'));

    expect(showPending.value).toBe(true);
    expect(pending.value).toMatchObject({ answer: 'answer', reasoning: 'thinking' });
    expect(busy.value).toBe(true);

    finishReload!({
      conversation: { id: 'A', title: 'A', model_id: 'm', pinned: false, message_count: 2, created_at: 0, updated_at: 0 },
      messages: [{ id: 'saved', seq: 2, role: 'assistant', content: 'answer', reasoning: 'thinking', created_at: 0 }],
    });
    await running;

    expect(showPending.value).toBe(false);
    expect(messages.value[0]).toMatchObject({ id: 'saved', content: 'answer', reasoning: 'thinking' });
    expect(recentReasoningID.value).toBe('saved');
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

  // The bubble's sent animation is bound to justSentID. It was assigned and
  // then cleared in the same tick, so Vue painted neither value and the
  // animation never played for anybody. It survives the turn now, and is
  // cleared where the message it names stops being on screen.
  it('keeps the sent marker while the turn it names is on screen', async () => {
    const running = runTurn({ content: 'hi' });

    const local = messages.value.find((message) => message.content === 'hi');
    expect(local, 'the optimistic row should be in the transcript').toBeTruthy();
    expect(justSentID.value).toBe(local!.id);

    start();
    expect(busy.value).toBe(true);
    // Still set, which is the whole point: something has to be able to render it.
    expect(justSentID.value).toBe(local!.id);

    turn.finish?.();
    await running;

    // Leaving the conversation clears it, because the row it names is gone.
    await openConversation('B');
    expect(justSentID.value).toBe('');
  });

  // The send button is in stop mode only while there is something to stop. The
  // answer is in by the time the saved row is being read, so the button must
  // not offer to stop a generation that has finished.
  it('leaves stop mode once the answer is in, while the saved row is still being read', async () => {
    let finishReload: ((value: Awaited<ReturnType<typeof getConversation>>) => void) | undefined;
    vi.mocked(getConversation).mockImplementationOnce(() => new Promise((resolve) => { finishReload = resolve; }));

    const running = runTurn({ content: 'hi' });
    start();
    expect(stoppable.value).toBe(true);

    turn.handlers?.onDone?.({ message_id: 'saved', stopped: false, streamed: true });
    turn.finish?.();
    await vi.waitFor(() => expect(finishReload).toBeTypeOf('function'));

    expect(busy.value).toBe(true);
    expect(stoppable.value).toBe(false);

    finishReload!({
      conversation: { id: 'A', title: 'A', model_id: 'm', pinned: false, message_count: 2, created_at: 0, updated_at: 0 },
      messages: [{ id: 'saved', seq: 2, role: 'assistant', content: 'answer', created_at: 0 }],
    });
    await running;
    expect(busy.value).toBe(false);
    expect(stoppable.value).toBe(false);
  });

  it('does not blank or reload the conversation that is already open when it is selected again', async () => {
    await openConversation('B');
    expect(messages.value[0]?.id).toBe('B-row');
    vi.mocked(getConversation).mockClear();

    // A double-click on a row selects it twice.
    await openConversation('B');
    await openConversation('B');

    expect(messages.value[0]?.id).toBe('B-row');
    expect(getConversation).not.toHaveBeenCalled();
  });

  it('says so when the open conversation cannot be read, rather than showing it as empty', async () => {
    const warn = vi.spyOn(console, 'warn').mockImplementation(() => {});
    vi.mocked(getConversation).mockRejectedValueOnce(new TypeError('Failed to fetch'));
    flash.value = '';

    await openConversation('B');

    expect(messages.value).toEqual([]);
    expect(activeID.value).toBe('B');
    expect(flash.value).toBe(t('failed'));
    warn.mockRestore();
  });

  // Archiving or deleting the open conversation used to clear only its id and
  // rows, so the block still streaming into it kept drawing on the blank
  // screen left behind until the turn ended.
  for (const [name, drop] of [['archiving', archiveConversation], ['deleting', removeConversation]] as const) {
    it(`stops drawing the streamed block after ${name} the conversation it streams into`, async () => {
      const running = runTurn({ content: 'hi' });
      start();
      expect(showPending.value).toBe(true);

      const entry = conversations.value.find((conversation) => conversation.id === 'A')!;
      expect(await drop(entry)).toBe(true);

      expect(activeID.value).toBe('');
      expect(messages.value).toEqual([]);
      expect(showPending.value).toBe(false);
      // The turn itself is not cut off: the server still saves it.
      expect(busy.value).toBe(true);

      turn.finish?.();
      await running;
      expect(busy.value).toBe(false);
      expect(messages.value).toEqual([]);
    });
  }
});
