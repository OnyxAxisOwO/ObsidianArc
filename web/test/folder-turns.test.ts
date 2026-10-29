// A conversation started inside a folder belongs to that folder from the
// first event the server sends, not from the next read of the rail: the stub
// the rail draws while the answer streams carries the folder the request
// named, even when the reader has pointed the composer somewhere else since —
// and a folder that was closed opens to show it, or the conversation being
// written into would have no row anywhere.

import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import { createApp, h, nextTick, ref, type App } from 'vue';

import type { TurnHandlers } from '../src/api/chat';
import type { Project } from '../src/api/projects';
import { providePanelHost } from '../src/composables/usePanelHost';

const turn: { request: Record<string, unknown> | null; handlers: TurnHandlers | null; finish: (() => void) | null } = {
  request: null,
  handlers: null,
  finish: null,
};

vi.mock('@/api/chat', async (importOriginal) => ({
  ...(await importOriginal<typeof import('../src/api/chat')>()),
  listConversations: vi.fn(async () => ({ conversations: [] })),
  getConversation: vi.fn(async (id: string) => ({
    conversation: { id, title: id, model_id: 'm', pinned: false, message_count: 1, created_at: 0, updated_at: 0 },
    messages: [],
  })),
  sendTurn: vi.fn((request: Record<string, unknown>, handlers: TurnHandlers) => {
    turn.request = request;
    turn.handlers = handlers;
    return new Promise<void>((resolve) => { turn.finish = resolve; });
  }),
}));

vi.mock('@/api/projects', async (importOriginal) => ({
  ...(await importOriginal<typeof import('../src/api/projects')>()),
  listProjects: vi.fn(),
}));

const { listConversations } = await import('../src/api/chat');
const { listProjects } = await import('../src/api/projects');
const ChatSidebar = (await import('../src/chat/ChatSidebar.vue')).default;
const { conversations, resetChat, runTurn } = await import('../src/chat/useChat');
const { models, selectedID } = await import('../src/chat/useModels');
const { projectList, resetProjects } = await import('../src/stores/projects');
const { pendingProjectID } = await import('../src/stores/workspace');

function folder(id: string, name: string, count: number): Project {
  return { id, name, instructions: '', conversations: count, created_at: 0, updated_at: 0 };
}

async function settle(): Promise<void> {
  for (let i = 0; i < 3; i += 1) {
    await new Promise((resolve) => setTimeout(resolve, 0));
    await nextTick();
  }
}

function start(id: string): void {
  turn.handlers?.onStart?.({ conversation_id: id, title: 'New one', model_id: 'm', model_name: 'M' });
}

let app: App | null = null;
let host: HTMLElement;

describe('a conversation started in a folder', () => {
  beforeEach(() => {
    resetChat();
    resetProjects();
    models.value = [{ id: 'm', display_name: 'M' } as (typeof models.value)[number]];
    selectedID.value = 'm';
    turn.request = null;
    turn.handlers = null;
    turn.finish = null;
    vi.mocked(listProjects).mockReset();
    vi.mocked(listProjects).mockImplementation(async () => ({
      projects: [folder('p1', 'Folder One', 0), folder('p2', 'Folder Two', 0)], max: 0,
    }));
    vi.mocked(listConversations).mockClear();
    document.body.textContent = '';
    host = document.createElement('div');
    document.body.appendChild(host);
  });

  afterEach(async () => {
    turn.finish?.();
    await settle();
    app?.unmount();
    app = null;
  });

  it('is filed in the folder the request named from the first event, wherever the composer points by then', async () => {
    pendingProjectID.value = 'p1';
    const running = runTurn({ content: 'hi' });
    expect(turn.request?.['project_id']).toBe('p1');

    // The reader points the composer at another folder before the server answers.
    pendingProjectID.value = 'p2';
    start('new-1');

    expect(conversations.value[0]).toMatchObject({ id: 'new-1', project_id: 'p1' });
    // Its folder's count is the server's, read again now rather than at the end.
    expect(listProjects).toHaveBeenCalled();
    turn.finish?.();
    await running;
  });

  it('is loose when the composer pointed at no folder', async () => {
    const running = runTurn({ content: 'hi' });
    expect(turn.request && 'project_id' in turn.request).toBe(false);
    start('new-2');

    expect(conversations.value[0]?.id).toBe('new-2');
    expect(conversations.value[0]?.project_id).toBeUndefined();
    expect(listProjects).not.toHaveBeenCalled();
    turn.finish?.();
    await running;
  });

  it('opens its folder in the rail and reads it, though the folder was closed', async () => {
    app = createApp({
      setup() {
        providePanelHost(ref(host));
        return () => h(ChatSidebar);
      },
    });
    app.mount(host);
    await vi.dynamicImportSettled();
    await settle();
    expect(projectList.value).toHaveLength(2);
    const block = [...host.querySelectorAll<HTMLElement>('.ai-folder')]
      .find((node) => node.dataset['folderId'] === 'p1')!;
    expect(block.querySelector('.ai-folder-open')?.getAttribute('aria-expanded')).toBe('false');

    vi.mocked(listConversations).mockImplementation(async (params) => ({
      conversations: params?.project_id === 'p1'
        ? [{ id: 'new-3', project_id: 'p1', title: 'New one', model_id: 'm', pinned: false, message_count: 1, created_at: 0, updated_at: 0 }]
        : [],
    }));
    pendingProjectID.value = 'p1';
    const running = runTurn({ content: 'hi' });
    start('new-3');
    await settle();

    expect(block.querySelector('.ai-folder-open')?.getAttribute('aria-expanded')).toBe('true');
    expect(listConversations).toHaveBeenCalledWith({ project_id: 'p1', archived: false });
    expect([...block.querySelectorAll('.ai-project-convs-wrap .ai-chat-list-title')].map((node) => node.textContent?.trim()))
      .toEqual(['New one']);
    // Not among the loose ones: it is drawn once, where it is filed.
    expect(host.querySelector('.ai-rail-loose .ai-chat-list-title')).toBeNull();
    turn.finish?.();
    await running;
  });
});
