import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import { createApp, h, nextTick, ref, type App } from 'vue';
import ChatSidebar from '../src/chat/ChatSidebar.vue';
import ChatSurface from '../src/chat/ChatSurface.vue';
import { draft, resetChat } from '../src/chat/useChat';
import { models, selectedID } from '../src/chat/useModels';
import { providePanelHost } from '../src/composables/usePanelHost';

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

let app: App | null = null;
let host: HTMLElement;

function configureModel(vision: boolean): void {
  models.value = [{ id: 'm', display_name: 'M', supports_images: vision } as (typeof models.value)[number]];
  selectedID.value = 'm';
}

/** A DragEvent-shaped event: jsdom has no DataTransfer to carry the files. */
function fileDrag(type: 'dragover' | 'drop', files: File[]): Event {
  const event = new Event(type, { bubbles: true, cancelable: true });
  Object.defineProperty(event, 'dataTransfer', {
    value: { types: ['Files'], files, dropEffect: 'none' },
  });
  return event;
}

describe('dropping files on the chat', () => {
  beforeEach(() => {
    document.body.textContent = '';
    host = document.createElement('div');
    document.body.appendChild(host);
    const surfaceHost = host;
    app = createApp({
      setup() {
        providePanelHost(ref(surfaceHost));
        return () => h(ChatSurface);
      },
    });
    app.mount(host);
  });

  afterEach(() => {
    app?.unmount();
    app = null;
    document.body.textContent = '';
    resetChat();
  });

  it.each([true, false])('cancels the browser default for a file drag, with vision %s', async (vision) => {
    configureModel(vision);
    await nextTick();
    const main = host.querySelector<HTMLElement>('.ai-chat-main')!;

    const dragOver = fileDrag('dragover', [new File(['x'], 'a.txt', { type: 'text/plain' })]);
    main.dispatchEvent(dragOver);
    expect(dragOver.defaultPrevented).toBe(true);

    const drop = fileDrag('drop', [new File(['x'], 'a.txt', { type: 'text/plain' })]);
    main.dispatchEvent(drop);
    expect(drop.defaultPrevented).toBe(true);
  });

  it('takes a dropped text file on a model that cannot read pictures, into the draft', async () => {
    configureModel(false);
    await nextTick();
    const main = host.querySelector<HTMLElement>('.ai-chat-main')!;
    const file = new File(['hello there'], 'notes.txt', { type: 'text/plain' });
    Object.defineProperty(file, 'text', { value: async () => 'hello there' });

    main.dispatchEvent(fileDrag('drop', [file]));
    await vi.waitFor(() => expect(draft.value).toContain('notes.txt'));
    expect(draft.value).toContain('hello there');
  });
});

describe('dropping files on the history rail', () => {
  let railApp: App | null = null;

  afterEach(() => {
    railApp?.unmount();
    railApp = null;
    document.body.textContent = '';
  });

  it('cancels the browser default, so a dropped file does not replace the chat', () => {
    const railHost = document.createElement('div');
    document.body.appendChild(railHost);
    railApp = createApp({
      setup() {
        providePanelHost(ref(railHost));
        return () => h(ChatSidebar);
      },
    });
    railApp.mount(railHost);
    const rail = railHost.querySelector<HTMLElement>('.ai-chat-sidebar')!;

    const dragOver = fileDrag('dragover', [new File(['x'], 'a.txt', { type: 'text/plain' })]);
    rail.dispatchEvent(dragOver);
    expect(dragOver.defaultPrevented).toBe(true);

    const drop = fileDrag('drop', [new File(['x'], 'a.txt', { type: 'text/plain' })]);
    rail.dispatchEvent(drop);
    expect(drop.defaultPrevented).toBe(true);
  });
});
