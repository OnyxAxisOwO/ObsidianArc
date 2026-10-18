import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import { createApp, type App } from 'vue';

import { ApiError } from '../src/api/client';
import { t } from '../src/composables/useI18n';
import { settle } from './helpers';

// A project the rail could not read says so in the reader's words. The
// server's sentence for its own fault is English written for its log, and a
// 5xx used to reach the chat's flash as written. A session that ended has a
// sentence of its own, and a refusal the server worded for the reader is shown
// as written.

let failure: unknown = null;

vi.mock('@/api/chat', async (importOriginal) => ({
  ...(await importOriginal<typeof import('../src/api/chat')>()),
  listConversations: vi.fn(async () => { throw failure; }),
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

describe('a project that could not be read', () => {
  beforeEach(() => {
    document.body.textContent = '';
    host = document.createElement('div');
    document.body.appendChild(host);
    flash.value = '';
    vi.spyOn(console, 'warn').mockImplementation(() => {});
  });

  afterEach(() => {
    app?.unmount();
    app = null;
    vi.restoreAllMocks();
  });

  async function openProject(): Promise<void> {
    app = createApp(ProjectList);
    app.mount(host);
    await settle();
    host.querySelector<HTMLButtonElement>('.ai-chat-list-open')!.click();
    await settle();
  }

  it('shows the generic line for a server fault, not the server’s sentence', async () => {
    failure = new ApiError(500, 'internal', 'pq: connection refused');
    await openProject();
    expect(flash.value).toBe(t('failed'));
  });

  it('shows the generic line for a failure that never reached the server', async () => {
    failure = new TypeError('Failed to fetch');
    await openProject();
    expect(flash.value).toBe(t('failed'));
  });

  it('shows the session-ended sentence once the session has gone', async () => {
    failure = new ApiError(401, 'session_required', 'Sign in again.');
    await openProject();
    expect(flash.value).toBe(t('errSessionEnded'));
  });

  it('keeps the server’s sentence for a refusal the reader can act on', async () => {
    failure = new ApiError(400, 'some_refusal', 'That project is full.');
    await openProject();
    expect(flash.value).toBe('That project is full.');
  });
});
