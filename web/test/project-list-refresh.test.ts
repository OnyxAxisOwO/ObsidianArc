import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import { createApp, nextTick, type App } from 'vue';

import type { Conversation } from '../src/api/chat';

// Every turn changes the rail's conversations, and the project tab refreshes
// each open project when they do. It used to swap the list already on screen
// for a spinner for the length of that request, so every open project
// flashed after each answer and a row menu open in it was unmounted.

const conv = (id: string): Conversation => ({
  id, title: id, model_id: 'm', pinned: false, message_count: 1, created_at: 0, updated_at: 0,
});

let answer: (value: { conversations: Conversation[] }) => void = () => {};

vi.mock('@/api/chat', async (importOriginal) => ({
  ...(await importOriginal<typeof import('../src/api/chat')>()),
  listConversations: vi.fn(() => new Promise((resolve) => { answer = resolve; })),
}));

vi.mock('@/api/projects', async (importOriginal) => ({
  ...(await importOriginal<typeof import('../src/api/projects')>()),
  listProjects: vi.fn(async () => ({
    projects: [{ id: 'p1', name: 'Project', instructions: '', conversations: 1, created_at: 0, updated_at: 0 }],
    max: 64,
  })),
}));

const ProjectList = (await import('../src/chat/ProjectList.vue')).default;
const { conversations } = await import('../src/chat/useChat');

let app: App | null = null;
let host: HTMLElement;

async function settle(): Promise<void> {
  for (let i = 0; i < 4; i += 1) {
    await Promise.resolve();
    await nextTick();
  }
}

describe('the project list after a turn', () => {
  beforeEach(() => {
    document.body.textContent = '';
    host = document.createElement('div');
    document.body.appendChild(host);
  });

  afterEach(() => {
    app?.unmount();
    app = null;
  });

  it('keeps an open project listed while it is read again', async () => {
    app = createApp(ProjectList);
    app.mount(host);
    await settle();

    host.querySelector<HTMLButtonElement>('.ai-chat-list-open')!.click();
    await settle();
    // The first read has nothing to show yet, so it is the spinner's.
    expect(host.querySelector('.ai-project-convs-wrap .ai-chat-spinner')).not.toBeNull();
    answer({ conversations: [conv('c1')] });
    await settle();
    expect(host.querySelector('.ai-project-conv-item')).not.toBeNull();

    // A turn ends: the rail is replaced, and the project is read again.
    conversations.value = [conv('c1')];
    await settle();
    expect(host.querySelector('.ai-project-conv-item')).not.toBeNull();
    expect(host.querySelector('.ai-project-convs-wrap .ai-chat-spinner')).toBeNull();

    answer({ conversations: [conv('c2'), conv('c1')] });
    await settle();
    expect(host.querySelectorAll('.ai-project-conv-item')).toHaveLength(2);
  });
});
