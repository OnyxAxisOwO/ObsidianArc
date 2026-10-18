import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import { createApp, type App } from 'vue';

import { ApiError } from '../src/api/client';
import type { Conversation } from '../src/api/chat';
import { t } from '../src/composables/useI18n';
import { settle } from './helpers';

// A failed read of an open project lands in the chat's one flash line. A server
// fault must not show its English sentence, a session that ended shows its own
// sentence, and a refusal the server worded for the reader is shown as written.

let refuse: (error: unknown) => void = () => {};

vi.mock('@/api/chat', async (importOriginal) => ({
  ...(await importOriginal<typeof import('../src/api/chat')>()),
  listConversations: vi.fn(
    () => new Promise<{ conversations: Conversation[] }>((_, reject) => { refuse = reject; }),
  ),
}));

vi.mock('@/api/projects', async (importOriginal) => ({
  ...(await importOriginal<typeof import('../src/api/projects')>()),
  listProjects: vi.fn(async () => ({
    projects: [{ id: 'p1', name: 'Project', instructions: '', conversations: 1, created_at: 0, updated_at: 0 }],
    max: 64,
  })),
}));

const ProjectList = (await import('../src/chat/ProjectList.vue')).default;
const { flash } = await import('../src/chat/useChat');

let app: App | null = null;
let host: HTMLElement;

async function failOpeningProject(error: ApiError): Promise<void> {
  app = createApp(ProjectList);
  app.mount(host);
  await settle();
  host.querySelector<HTMLButtonElement>('.ai-chat-list-open')!.click();
  await settle();
  refuse(error);
  await settle();
}

describe('the project list when a read fails', () => {
  beforeEach(() => {
    document.body.textContent = '';
    host = document.createElement('div');
    document.body.appendChild(host);
    flash.value = '';
  });

  afterEach(() => {
    app?.unmount();
    app = null;
  });

  it('shows the generic line for a server fault instead of its sentence', async () => {
    await failOpeningProject(new ApiError(500, 'internal', 'pq: deadlock detected'));
    expect(flash.value).toBe(t('failed'));
  });

  it('shows the session-ended sentence once the session has gone', async () => {
    await failOpeningProject(new ApiError(401, 'session_required', 'Sign in again.'));
    expect(flash.value).toBe(t('errSessionEnded'));
  });

  it('shows a refusal as the server worded it', async () => {
    await failOpeningProject(new ApiError(404, 'not_found', 'No such project.'));
    expect(flash.value).toBe('No such project.');
  });
});
