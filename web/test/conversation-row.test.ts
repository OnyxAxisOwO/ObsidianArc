// A conversation's row, shared by the history rail and the project lists:
// renaming happens in the row rather than in the browser's prompt(), which
// some browsers suppress, and the delete in its menu asks once before it acts.

import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import { createApp, defineComponent, h, nextTick, reactive, type App } from 'vue';
import { renameConversation, type Conversation } from '../src/api/chat';
import ConversationRow from '../src/chat/ConversationRow.vue';
import { conversations, flash, renamingID } from '../src/chat/useChat';
import { useRowMenu } from '../src/chat/useRowMenu';

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
  return { id, title, model_id: 'm', pinned: false, message_count: 0, created_at: 0, updated_at: 0 } as Conversation;
}

let app: App | null = null;
let host: HTMLElement;
let row: Conversation;
let removed = 0;

// Not mountComponent: the row's menu comes from a composable that has to run in the same setup that draws the row.
function mount(): void {
  host = document.createElement('div');
  document.body.appendChild(host);
  app = createApp(defineComponent({
    setup() {
      const menu = useRowMenu();
      return () => h(ConversationRow, {
        conversation: row,
        menu,
        active: false,
        pending: false,
        onRemove: () => { removed += 1; },
      });
    },
  }));
  app.mount(host);
}

async function settle(): Promise<void> {
  await nextTick();
  await nextTick();
}

function field(): HTMLInputElement | null {
  return host.querySelector<HTMLInputElement>('.ai-project-form input');
}

async function startRename(): Promise<HTMLInputElement> {
  host.querySelector<HTMLButtonElement>('.ai-chat-list-open')!.dispatchEvent(new MouseEvent('dblclick', { bubbles: true }));
  await settle();
  const input = field();
  expect(input).not.toBeNull();
  return input!;
}

function type(input: HTMLInputElement, value: string): void {
  input.value = value;
  input.dispatchEvent(new Event('input'));
}

beforeEach(() => {
  vi.spyOn(console, 'warn').mockImplementation(() => {});
  // Reactive, as both lists' rows are: the rename writes the title onto it.
  row = reactive(conversation('c1', 'Old'));
  conversations.value = [conversation('c1', 'Old')];
  removed = 0;
  flash.value = '';
});

afterEach(() => {
  app?.unmount();
  app = null;
  vi.restoreAllMocks();
  vi.mocked(renameConversation).mockReset();
  renamingID.value = '';
  conversations.value = [];
});

describe('renaming in the row', () => {
  it('opens a field holding the title on a double click, and saves it on Enter', async () => {
    vi.mocked(renameConversation).mockResolvedValue({ conversation: conversation('c1', 'New') });
    mount();
    const input = await startRename();
    expect(input.value).toBe('Old');
    expect(document.activeElement).toBe(input);

    type(input, 'New');
    input.dispatchEvent(new KeyboardEvent('keydown', { key: 'Enter', bubbles: true, cancelable: true }));
    // The field going takes focus with it; that blur must not save a second time.
    input.dispatchEvent(new FocusEvent('focusout', { bubbles: true }));
    await settle();

    expect(renameConversation).toHaveBeenCalledTimes(1);
    expect(renameConversation).toHaveBeenCalledWith('c1', 'New');
    expect(field()).toBeNull();
    expect(host.querySelector('.ai-chat-list-title')?.textContent).toBe('New');
    expect(conversations.value[0]?.title).toBe('New');
  });

  it('leaves the title alone on Escape, and marks the key handled', async () => {
    mount();
    const input = await startRename();
    type(input, 'Something else');
    const escape = new KeyboardEvent('keydown', { key: 'Escape', bubbles: true, cancelable: true });
    input.dispatchEvent(escape);
    input.dispatchEvent(new FocusEvent('focusout', { bubbles: true }));
    await settle();

    expect(escape.defaultPrevented).toBe(true);
    expect(renameConversation).not.toHaveBeenCalled();
    expect(field()).toBeNull();
    expect(host.querySelector('.ai-chat-list-title')?.textContent).toBe('Old');
  });

  it('saves on leaving the field, and shows a refused rename in the flash', async () => {
    vi.mocked(renameConversation).mockRejectedValue(new TypeError('Failed to fetch'));
    mount();
    const input = await startRename();
    type(input, 'New');
    input.dispatchEvent(new FocusEvent('focusout', { bubbles: true }));
    await settle();

    expect(renameConversation).toHaveBeenCalledWith('c1', 'New');
    expect(flash.value).not.toBe('');
    expect(host.querySelector('.ai-chat-list-title')?.textContent).toBe('Old');
  });

  it('does not take an input method\'s Enter as the end of typing', async () => {
    mount();
    const input = await startRename();
    input.dispatchEvent(new KeyboardEvent('keydown', { key: 'Enter', isComposing: true, bubbles: true }));
    await settle();
    expect(renameConversation).not.toHaveBeenCalled();
    expect(field()).not.toBeNull();
  });
});

describe('deleting from the row menu', () => {
  it('arms on the first press, keeping the menu open, and deletes on the second', async () => {
    mount();
    host.querySelector<HTMLButtonElement>('.ai-chat-list-more')!.click();
    await settle();
    const items = () => Array.from(host.querySelectorAll<HTMLElement>('.ai-chat-context-menu button'));
    const deleteItem = () => items()[items().length - 1]!;
    deleteItem().click();
    await settle();
    expect(removed).toBe(0);
    expect(host.querySelector('.ai-chat-list-more')?.getAttribute('aria-expanded')).toBe('true');

    deleteItem().click();
    await settle();
    expect(removed).toBe(1);
    expect(host.querySelector('.ai-chat-list-more')?.getAttribute('aria-expanded')).toBe('false');
  });
});
