import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import { createApp, type App } from 'vue';
import type { Conversation } from '../src/api/chat';
import { listConversations } from '../src/api/chat';
import type { Project } from '../src/api/projects';
import { listProjects } from '../src/api/projects';
import ProjectList from '../src/chat/ProjectList.vue';
import * as chatModule from '../src/chat/useChat';
import { projectList } from '../src/stores/projects';
import { pendingProjectID, setProject } from '../src/stores/workspace';

vi.mock('@/api/chat', () => ({
  archiveConversation: vi.fn(async () => undefined),
  deleteAllConversations: vi.fn(async () => undefined),
  deleteConversation: vi.fn(async () => undefined),
  getConversation: vi.fn(async () => ({ conversation: undefined, messages: [] })),
  listConversations: vi.fn(),
  renameConversation: vi.fn(),
  sendTurn: vi.fn(),
  uploadAttachment: vi.fn(),
}));

vi.mock('@/api/projects', () => ({
  createProject: vi.fn(),
  deleteProject: vi.fn(),
  listProjects: vi.fn(),
  updateProject: vi.fn(),
}));

const PROJECT: Project = {
  id: 'p1',
  name: 'Project One',
  instructions: '',
  conversations: 2,
  created_at: 0,
  updated_at: 0,
};

function conversation(id: string, title: string): Conversation {
  return { id, project_id: 'p1', title, model_id: 'm1', pinned: false, message_count: 1, created_at: 0, updated_at: 0 };
}

let app: App | null = null;
let host: HTMLElement;

async function settle(): Promise<void> {
  await Promise.resolve();
  await new Promise((resolve) => requestAnimationFrame(resolve));
  await Promise.resolve();
}

function openButton(): HTMLButtonElement {
  return host.querySelector<HTMLButtonElement>('.ai-chat-list-open')!;
}

describe('ProjectList expansion', () => {
  beforeEach(() => {
    document.body.textContent = '';
    host = document.createElement('div');
    document.body.appendChild(host);
    chatModule.conversations.value = [];
    projectList.value = [PROJECT];
    pendingProjectID.value = '';
    vi.mocked(listProjects).mockResolvedValue({ projects: [PROJECT], max: 0 });
    vi.mocked(listConversations).mockReset();
    vi.mocked(listConversations).mockResolvedValue({ conversations: [] });
    if (!Element.prototype.scrollIntoView) Element.prototype.scrollIntoView = () => {};
  });

  afterEach(() => {
    app?.unmount();
    app = null;
    document.body.textContent = '';
  });

  it('shows the newest list when an older request for the same project resolves last', async () => {
    let resolveOlder: (value: { conversations: Conversation[] }) => void = () => {};
    vi.mocked(listConversations)
      .mockImplementationOnce(() => new Promise((resolve) => { resolveOlder = resolve; }))
      .mockImplementationOnce(async () => ({ conversations: [conversation('c-new', 'Newer chat')] }));

    app = createApp(ProjectList);
    app.mount(host);
    await settle();

    // Expanding starts a load that stays pending...
    openButton().click();
    await settle();

    // ...and a change to the conversations starts a second one, which lands first.
    chatModule.conversations.value = [conversation('c-new', 'Newer chat')];
    await settle();
    expect(vi.mocked(listConversations)).toHaveBeenCalledTimes(2);
    expect(host.textContent).toContain('Newer chat');

    resolveOlder({ conversations: [conversation('c-old', 'Older chat')] });
    await settle();

    expect(host.textContent).toContain('Newer chat');
    expect(host.textContent).not.toContain('Older chat');
  });

  it('leaves the pending project alone when a project is collapsed', async () => {
    app = createApp(ProjectList);
    app.mount(host);
    await settle();

    openButton().click();
    await settle();
    expect(pendingProjectID.value).toBe('p1');

    // Something else takes the target while the project stays open...
    setProject('');

    // ...and closing it must not put the project back as the target of the next chat.
    openButton().click();
    await settle();
    expect(pendingProjectID.value).toBe('');
  });
});
