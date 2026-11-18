import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import { createApp, type App } from 'vue';

import * as projectsApi from '../src/api/projects';
import { t } from '../src/composables/useI18n';
import type { Conversation } from '../src/api/chat';
import { settle } from './helpers';

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
const { loadProjects, resetProjects } = await import('../src/stores/projects');
const { conversations } = await import('../src/chat/useChat');

let app: App | null = null;
let host: HTMLElement;


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

  it('saves both fields from the submitted project and leaves a later editor focused', async () => {
    const a = { id: 'a', name: 'Alpha', instructions: 'First', conversations: 0, created_at: 0, updated_at: 0 };
    const b = { ...a, id: 'b', name: 'Beta', instructions: 'Second' };
    vi.mocked(projectsApi.listProjects).mockResolvedValueOnce({ projects: [a, b], max: 64 });
    let finish!: (record: projectsApi.Project) => void;
    const update = vi.spyOn(projectsApi, 'updateProject').mockReturnValue(new Promise((resolve) => { finish = resolve; }));
    resetProjects();
    await loadProjects();
    app = createApp(ProjectList);
    app.mount(host);
    await settle();
    const edit = (id: string) => host.querySelector<HTMLButtonElement>(`[data-folder-id="${id}"] [aria-label="${t('editProject')}"]`)!;
    edit(a.id).click();
    await settle();
    const name = host.querySelector<HTMLInputElement>('.ai-project-form input')!;
    expect(document.activeElement).toBe(name);
    name.value = 'Revised';
    name.dispatchEvent(new Event('input', { bubbles: true }));
    const instructions = host.querySelector<HTMLTextAreaElement>('.ai-project-form textarea')!;
    instructions.value = 'Revised brief';
    instructions.dispatchEvent(new Event('input', { bubbles: true }));
    await settle();
    host.querySelector<HTMLButtonElement>('.ai-project-form .primary')!.click();
    await settle();
    expect(update).toHaveBeenCalledWith(a.id, { name: 'Revised', instructions: 'Revised brief' });
    edit(b.id).click();
    await settle();
    finish({ ...a, name: 'Revised', instructions: 'Revised brief' });
    await settle();
    const next = host.querySelector<HTMLInputElement>('.ai-project-form input')!;
    expect(next.value).toBe(b.name);
    expect(document.activeElement).toBe(next);
    expect(update).toHaveBeenCalledTimes(1);
    update.mockRestore();
  });

  it('keeps an open project listed while it is read again', async () => {
    resetProjects();
    await loadProjects(); // The rail loads the folder list before mounting its content.
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
