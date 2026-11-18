import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import { sendTurn, type TurnHandlers } from '../src/api/chat';
import type { SiteInfo } from '../src/api/auth';
import { openConversation, pending, resetChat, runTurn, webSearchOn } from '../src/chat/useChat';
import { models, selectedID } from '../src/chat/useModels';
import { site } from '../src/stores/session';

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

describe('turning web search on for one message', () => {
  let handlers: TurnHandlers | undefined;
  let finish: (() => void) | undefined;

  beforeEach(() => {
    models.value = [{ id: 'm', display_name: 'M' } as (typeof models.value)[number]];
    selectedID.value = 'm';
    site.value = { web_search: true } as unknown as SiteInfo;
    webSearchOn.value = true;
    handlers = undefined;
    finish = undefined;
    vi.mocked(sendTurn).mockImplementation((_request, next) => new Promise<void>((resolve) => {
      handlers = next;
      finish = () => {
        next.onDelta?.('the answer');
        next.onDone?.({ message_id: 'm1', stopped: false, streamed: true });
        resolve();
      };
    }));
  });

  afterEach(() => {
    resetChat();
    site.value = null;
    vi.mocked(sendTurn).mockReset();
  });

  it('sends the flag and turns the server search notice into the waiting line', async () => {
    const running = runTurn({ content: 'what changed?' });
    await vi.waitFor(() => expect(handlers).toBeDefined());

    expect(vi.mocked(sendTurn).mock.calls[0]?.[0]).toMatchObject({
      content: 'what changed?',
      web_search: true,
    });
    // The switch is for this message only; the next turn starts from off.
    expect(webSearchOn.value).toBe(false);
    expect(pending.value?.search).toBeNull();

    handlers!.onSearch!({ searching: true, results: 0 });
    expect(pending.value?.search).toEqual({ state: 'searching', count: 0 });

    handlers!.onSearch!({ results: 2 });
    expect(pending.value?.search).toEqual({ state: 'found', count: 2 });

    finish!();
    await running;
    // The block, and the notice on it, go when the saved row takes over.
    expect(pending.value).toBeNull();
  });

  it('says so when the search failed, while still taking the answer', async () => {
    const running = runTurn({ content: 'what changed?' });
    await vi.waitFor(() => expect(handlers).toBeDefined());

    handlers!.onSearch!({ results: 0, failed: true });
    expect(pending.value?.search).toEqual({ state: 'failed', count: 0 });

    finish!();
    await running;
    expect(pending.value).toBeNull();
  });

  it('does not send the flag when the switch is off', async () => {
    webSearchOn.value = false;
    const running = runTurn({ content: 'what changed?' });
    await vi.waitFor(() => expect(handlers).toBeDefined());

    expect(vi.mocked(sendTurn).mock.calls[0]?.[0]).not.toHaveProperty('web_search');
    expect(pending.value?.search).toBeNull();

    finish!();
    await running;
  });

  it('does not carry the switch into another conversation', async () => {
    webSearchOn.value = true;

    await openConversation('B');

    expect(webSearchOn.value).toBe(false);
  });
});
