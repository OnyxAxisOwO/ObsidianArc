import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import { createApp, h, nextTick, ref, type App } from 'vue';
import { listConversations, renameConversation, type Conversation } from '../src/api/chat';
import { listProjects, type Project } from '../src/api/projects';
import ChatSidebar from '../src/chat/ChatSidebar.vue';
import ProjectList from '../src/chat/ProjectList.vue';
import { active, activeID, conversations, flash, rename } from '../src/chat/useChat';
import { t } from '../src/composables/useI18n';
import { providePanelHost } from '../src/composables/usePanelHost';
import { projectList } from '../src/stores/projects';
import { pendingProjectID } from '../src/stores/workspace';

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

vi.mock('@/api/projects', () => ({
  createProject: vi.fn(),
  deleteProject: vi.fn(),
  listProjects: vi.fn(),
  updateProject: vi.fn(),
}));

function conversation(id: string, title: string): Conversation {
  return {
    id,
    title,
    model_id: 'm',
    pinned: false,
    message_count: 0,
    created_at: 0,
    updated_at: 0,
  } as Conversation;
}

let app: App | null = null;
let host: HTMLElement;

async function settle(): Promise<void> {
  await Promise.resolve();
  await new Promise((resolve) => requestAnimationFrame(resolve));
  await Promise.resolve();
  await nextTick();
}

function mountSidebar(): void {
  const panelHost = host;
  app = createApp({
    setup() {
      providePanelHost(ref(panelHost));
      return () => h(ChatSidebar);
    },
  });
  app.mount(host);
}

function field(): HTMLInputElement | null {
  return host.querySelector<HTMLInputElement>('.ai-project-form input');
}

function type(input: HTMLInputElement, value: string): void {
  input.value = value;
  input.dispatchEvent(new Event('input', { bubbles: true }));
}

function press(input: HTMLInputElement, key: string, init: KeyboardEventInit = {}): KeyboardEvent {
  const event = new KeyboardEvent('keydown', { key, bubbles: true, cancelable: true, ...init });
  input.dispatchEvent(event);
  return event;
}

beforeEach(() => {
  vi.spyOn(console, 'warn').mockImplementation(() => {});
  document.body.textContent = '';
  host = document.createElement('div');
  document.body.appendChild(host);
  flash.value = '';
  activeID.value = 'c1';
  conversations.value = [conversation('c1', 'Old')];
  vi.mocked(renameConversation).mockReset();
  if (!Element.prototype.scrollIntoView) Element.prototype.scrollIntoView = () => {};
});

afterEach(() => {
  app?.unmount();
  app = null;
  document.body.textContent = '';
  vi.restoreAllMocks();
  conversations.value = [];
  activeID.value = '';
  flash.value = '';
});

describe('renaming a conversation', () => {
  it('renames the rail and header copy when the reader renames it from a project row', async () => {
    // The project list fetches its own copies of its conversations, so the row
    // it renames is not the object the rail and the header read.
    const projectRow = conversation('c1', 'Old');
    vi.mocked(renameConversation).mockResolvedValue({ conversation: conversation('c1', 'New') });

    await rename(projectRow, '  New ');

    expect(renameConversation).toHaveBeenCalledWith('c1', 'New');
    expect(projectRow.title).toBe('New');
    expect(conversations.value[0]?.title).toBe('New');
    expect(active.value?.title).toBe('New');
  });

  it('shows the new title before the server answers', async () => {
    let answer: (value: { conversation: Conversation }) => void = () => {};
    vi.mocked(renameConversation).mockImplementation(() => new Promise((resolve) => { answer = resolve; }));

    const pending = rename(conversations.value[0]!, 'New');
    expect(conversations.value[0]?.title).toBe('New');

    answer({ conversation: conversation('c1', 'New') });
    await pending;
    expect(conversations.value[0]?.title).toBe('New');
  });

  it('puts the old title back and shows the translated failure when the server refuses', async () => {
    const projectRow = conversation('c1', 'Old');
    vi.mocked(renameConversation).mockRejectedValue(new TypeError('Failed to fetch'));

    await rename(projectRow, 'New');

    expect(flash.value).toBe(t('failed'));
    expect(flash.value).not.toContain('TypeError');
    expect(projectRow.title).toBe('Old');
    expect(conversations.value[0]?.title).toBe('Old');
  });

  it.each([['an empty', '   '], ['an unchanged', 'Old']])('sends nothing for %s name', async (_, title) => {
    await rename(conversations.value[0]!, title);

    expect(renameConversation).not.toHaveBeenCalled();
    expect(conversations.value[0]?.title).toBe('Old');
  });
});

describe('renaming in place on the rail', () => {
  it('opens a field on double-click instead of window.prompt, and Enter saves it', async () => {
    const prompt = vi.spyOn(window, 'prompt');
    vi.mocked(renameConversation).mockResolvedValue({ conversation: conversation('c1', 'New') });
    mountSidebar();
    await settle();

    host.querySelector<HTMLButtonElement>('.ai-chat-list-open')!.dispatchEvent(new MouseEvent('dblclick', { bubbles: true }));
    await settle();
    const input = field()!;
    expect(input).not.toBeNull();
    expect(input.value).toBe('Old');
    expect(document.activeElement).toBe(input);

    type(input, 'New');
    press(input, 'Enter');
    await settle();

    expect(field()).toBeNull();
    expect(renameConversation).toHaveBeenCalledTimes(1);
    expect(renameConversation).toHaveBeenCalledWith('c1', 'New');
    expect(host.querySelector('.ai-chat-list-title')?.textContent).toBe('New');
    expect(prompt).not.toHaveBeenCalled();
  });

  it('opens the field from the row menu', async () => {
    mountSidebar();
    await settle();

    host.querySelector<HTMLButtonElement>('.ai-chat-list-more')!.click();
    await settle();
    const item = [...document.querySelectorAll<HTMLButtonElement>('button.oa-menu-item')]
      .find((node) => node.textContent?.trim() === t('rename'));
    item!.click();
    await settle();

    expect(field()).not.toBeNull();
  });

  it('cancels on Escape without sending anything, and keeps the Escape to itself', async () => {
    mountSidebar();
    await settle();
    host.querySelector<HTMLButtonElement>('.ai-chat-list-open')!.dispatchEvent(new MouseEvent('dblclick', { bubbles: true }));
    await settle();

    const seen = vi.fn();
    document.addEventListener('keydown', seen);
    const input = field()!;
    type(input, 'Discarded');
    press(input, 'Escape');
    await settle();
    document.removeEventListener('keydown', seen);

    expect(field()).toBeNull();
    expect(seen).not.toHaveBeenCalled();
    expect(renameConversation).not.toHaveBeenCalled();
    expect(host.querySelector('.ai-chat-list-title')?.textContent).toBe('Old');
  });

  it('saves when the field loses focus, once', async () => {
    vi.mocked(renameConversation).mockResolvedValue({ conversation: conversation('c1', 'Blurred') });
    mountSidebar();
    await settle();
    host.querySelector<HTMLButtonElement>('.ai-chat-list-open')!.dispatchEvent(new MouseEvent('dblclick', { bubbles: true }));
    await settle();

    const input = field()!;
    type(input, 'Blurred');
    input.blur();
    await settle();

    expect(field()).toBeNull();
    expect(renameConversation).toHaveBeenCalledTimes(1);
    expect(renameConversation).toHaveBeenCalledWith('c1', 'Blurred');
  });

  it('leaves an Enter that an input method is still composing with alone', async () => {
    mountSidebar();
    await settle();
    host.querySelector<HTMLButtonElement>('.ai-chat-list-open')!.dispatchEvent(new MouseEvent('dblclick', { bubbles: true }));
    await settle();

    const input = field()!;
    type(input, '新');
    press(input, 'Enter', { isComposing: true });
    await settle();

    expect(field()).not.toBeNull();
    expect(renameConversation).not.toHaveBeenCalled();
  });

  it('does not save an empty name', async () => {
    mountSidebar();
    await settle();
    host.querySelector<HTMLButtonElement>('.ai-chat-list-open')!.dispatchEvent(new MouseEvent('dblclick', { bubbles: true }));
    await settle();

    const input = field()!;
    type(input, '  ');
    press(input, 'Enter');
    await settle();

    expect(field()).toBeNull();
    expect(renameConversation).not.toHaveBeenCalled();
    expect(host.querySelector('.ai-chat-list-title')?.textContent).toBe('Old');
  });

  it('rolls the row back and says so when the server refuses', async () => {
    vi.mocked(renameConversation).mockRejectedValue(new TypeError('Failed to fetch'));
    mountSidebar();
    await settle();
    host.querySelector<HTMLButtonElement>('.ai-chat-list-open')!.dispatchEvent(new MouseEvent('dblclick', { bubbles: true }));
    await settle();

    const input = field()!;
    type(input, 'New');
    press(input, 'Enter');
    await settle();

    expect(host.querySelector('.ai-chat-list-title')?.textContent).toBe('Old');
    expect(flash.value).toBe(t('failed'));
  });
});

describe('renaming in place in a project', () => {
  const PROJECT: Project = {
    id: 'p1',
    name: 'Project One',
    instructions: '',
    conversations: 1,
    created_at: 0,
    updated_at: 0,
  };

  it('opens the same field on a project conversation row and saves it with Enter', async () => {
    const prompt = vi.spyOn(window, 'prompt');
    projectList.value = [PROJECT];
    pendingProjectID.value = '';
    vi.mocked(listProjects).mockResolvedValue({ projects: [PROJECT], max: 0 });
    vi.mocked(listConversations).mockResolvedValue({ conversations: [{ ...conversation('c1', 'Old'), project_id: 'p1' }] });
    vi.mocked(renameConversation).mockResolvedValue({ conversation: conversation('c1', 'New') });
    app = createApp(ProjectList);
    app.mount(host);
    await settle();

    host.querySelector<HTMLButtonElement>('.ai-chat-list-open')!.click();
    await settle();
    host.querySelector<HTMLButtonElement>('.ai-project-conv-item .ai-chat-list-open')!
      .dispatchEvent(new MouseEvent('dblclick', { bubbles: true }));
    await settle();

    const input = field()!;
    expect(input.value).toBe('Old');
    type(input, 'New');
    press(input, 'Enter');
    await settle();

    expect(field()).toBeNull();
    expect(renameConversation).toHaveBeenCalledWith('c1', 'New');
    expect(conversations.value[0]?.title).toBe('New');
    expect(prompt).not.toHaveBeenCalled();
  });
});
