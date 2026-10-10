import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';

import type { Account } from '../src/api/auth';
import { adminApi } from '../src/admin/api';
import type { AttachmentRef, Conversation } from '../src/api/chat';
import { deleteAllConversations, listConversations, sendTurn, uploadAttachment } from '../src/api/chat';
import { fetchFeedbackUnread } from '../src/api/feedback';
import type { Project, ProjectList } from '../src/api/projects';
import { createProject as apiCreateProject, listProjects } from '../src/api/projects';
import type { PreparedImage } from '../src/chat/image';
import { prepareImage } from '../src/chat/image';
import { adopt, forget } from '../src/stores/session';

// Chat and project state live in module-level refs, so nothing else clears
// them when the account changes: a sign-out that does not reset them shows
// the next account the last one's transcript, draft and project list.

vi.mock('@/api/chat', () => ({
  archiveConversation: vi.fn(async () => undefined),
  deleteAllConversations: vi.fn(async () => undefined),
  deleteConversation: vi.fn(async () => undefined),
  getConversation: vi.fn(async () => ({ conversation: undefined, messages: [] })),
  listConversations: vi.fn(async () => ({ conversations: [] })),
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

vi.mock('@/api/feedback', () => ({
  fetchFeedbackUnread: vi.fn(async () => ({ unread: 0 })),
}));

// Decoding needs a canvas, which jsdom does not have. The test is about what
// happens around the decode, so the decode itself is handed in.
vi.mock('../src/chat/image', async (original) => ({
  ...(await original<typeof import('../src/chat/image')>()),
  prepareImage: vi.fn(),
}));

const {
  activeID, addImages, addTextFiles, attachments, busy, clearEverything, conversations, draft, flash, messages,
  pending, pendingID, refreshList, resetChat, runTurn, status,
} = await import('../src/chat/useChat');
const {
  loadModels, models, reasoning, restorePreferences, selectedID,
} = await import('../src/chat/useModels');
const { pendingProjectID } = await import('../src/stores/workspace');
const { createProject, loadProjects, projectList, projectMax } = await import('../src/stores/projects');
const { feedbackUnread, refreshFeedbackUnread, setFeedbackUnread } = await import('../src/stores/feedback');
const { pricedModels } = await import('../src/views/admin/shared');

function account(id: string): Account {
  return {
    id, username: id, email: '', nickname: '', avatar: '', bio: '', role: 'user',
    group_id: '', group_expires_at: 0, group_name: '', status: 'active', created_at: 0, updated_at: 0,
    last_login_at: 0, email_verified: true, allow_stats: true, allow_delete_conversations: true,
    api_restricted: false, api_restricted_until: 0, api_restriction_source: '',
  };
}

const alice = account('alice');
const bob = account('bob');

function conversation(id: string, title: string): Conversation {
  return { id, title, model_id: 'm', pinned: false, message_count: 1, created_at: 0, updated_at: 0 };
}

function project(id: string, name: string): Project {
  return { id, name, instructions: `${name} instructions`, conversations: 0, created_at: 0, updated_at: 0 };
}

/** A configured model that takes pictures, which is what the composer needs to accept both. */
function configureModel(): void {
  models.value = [{
    id: 'm', display_name: 'M', supports_images: true, supports_reasoning: false,
  } as (typeof models.value)[number]];
  selectedID.value = 'm';
  expect(status.value.configured).toBe(true);
  expect(status.value.vision).toBe(true);
}

const revoke = vi.fn();
const urlStatics = URL as unknown as { revokeObjectURL: (url: string) => void };
let originalRevoke: (url: string) => void;

beforeEach(() => {
  originalRevoke = urlStatics.revokeObjectURL;
  urlStatics.revokeObjectURL = revoke;
  vi.mocked(listConversations).mockResolvedValue({ conversations: [] });
  vi.mocked(listProjects).mockResolvedValue({ projects: [], max: 0 });
  configureModel();
});

afterEach(() => {
  forget();
  resetChat();
  urlStatics.revokeObjectURL = originalRevoke;
  vi.unstubAllGlobals();
  vi.clearAllMocks();
});

describe('signing out leaves nothing of the account behind', () => {
  it('clears the open conversation, the draft, what is staged and the pending project', () => {
    adopt(alice);
    activeID.value = 'conv-a';
    messages.value = [{ id: 'm1', seq: 1, role: 'user', content: 'alice only', created_at: 0 }];
    conversations.value = [conversation('conv-a', 'Alice only')];
    draft.value = 'half-written thought';
    attachments.value = [{
      ref: { id: 'att-a', mime: 'image/png', width: 1, height: 1, size: 1 } as AttachmentRef,
      preview: 'blob:alice-staged',
    }];
    pendingProjectID.value = 'proj-a';

    forget();

    expect(messages.value).toEqual([]);
    expect(conversations.value).toEqual([]);
    expect(activeID.value).toBe('');
    expect(draft.value).toBe('');
    expect(attachments.value).toEqual([]);
    expect(pendingProjectID.value).toBe('');
    expect(revoke).toHaveBeenCalledWith('blob:alice-staged');
  });

  it('ends a turn that is still streaming, so the next account does not watch it arrive', async () => {
    adopt(alice);
    let turnSignal: AbortSignal | undefined;
    vi.mocked(sendTurn).mockImplementationOnce((_request, _handlers, signal) => {
      turnSignal = signal;
      return new Promise<void>((_resolve, reject) => {
        signal.addEventListener('abort', () => reject(new DOMException('aborted', 'AbortError')));
      });
    });

    const turn = runTurn({ content: 'a question' });
    expect(busy.value).toBe(true);
    expect(pending.value).not.toBeNull();

    forget();

    expect(turnSignal?.aborted).toBe(true);
    expect(busy.value).toBe(false);
    expect(pending.value).toBeNull();
    expect(pendingID.value).toBe('');
    expect(messages.value).toEqual([]);
    await expect(turn).resolves.toBe('done');
    // The turn finishing after the reset must not put anything back.
    expect(messages.value).toEqual([]);
    expect(busy.value).toBe(false);
  });

  it('drops a conversation list that was requested before sign-out', async () => {
    adopt(alice);
    let answer!: (value: { conversations: Conversation[] }) => void;
    vi.mocked(listConversations).mockReturnValueOnce(new Promise((resolve) => { answer = resolve; }));

    const refreshing = refreshList();
    forget();
    answer({ conversations: [conversation('conv-a', 'Alice only')] });
    await refreshing;

    expect(conversations.value).toEqual([]);
  });

  it('keeps text read from a file before sign-out out of the next draft', async () => {
    adopt(alice);
    let readText!: (text: string) => void;
    const file = {
      name: 'notes.txt',
      size: 5,
      text: () => new Promise<string>((resolve) => { readText = resolve; }),
    };

    const adding = addTextFiles([file as unknown as File]);
    forget();
    readText('alice secret');
    await adding;

    expect(draft.value).toBe('');
  });

  it('keeps a picture decoded before sign-out from being uploaded under the next session', async () => {
    adopt(alice);
    let decoded!: (picture: PreparedImage) => void;
    vi.mocked(prepareImage).mockReturnValueOnce(new Promise((resolve) => { decoded = resolve; }));

    const adding = addImages([new File(['not a real picture'], 'a.png', { type: 'image/png' })]);
    forget();
    decoded({ mime: 'image/png', data: 'AAAA', width: 1, height: 1, bytes: 3, previewURL: 'blob:alice-picture' });
    await adding;

    expect(uploadAttachment).not.toHaveBeenCalled();
    expect(attachments.value).toEqual([]);
    expect(revoke).toHaveBeenCalledWith('blob:alice-picture');
  });

  it('shows the next account its own projects, fetched after the sign-out', async () => {
    adopt(alice);
    const aliceList: ProjectList = { projects: [project('proj-a', 'Alice')], max: 5 };
    vi.mocked(listProjects).mockResolvedValueOnce(aliceList);
    await loadProjects();
    expect(projectList.value.map((entry) => entry.id)).toEqual(['proj-a']);

    forget();
    expect(projectList.value).toEqual([]);
    expect(projectMax.value).toBe(0);

    adopt(bob);
    const bobList: ProjectList = { projects: [project('proj-b', 'Bob')], max: 5 };
    vi.mocked(listProjects).mockResolvedValueOnce(bobList);
    await loadProjects();
    expect(listProjects).toHaveBeenCalledTimes(2);
    expect(projectList.value.map((entry) => entry.id)).toEqual(['proj-b']);
    expect(projectMax.value).toBe(5);
  });

  it('drops a project list that was requested before sign-out', async () => {
    adopt(alice);
    let answer!: (value: ProjectList) => void;
    vi.mocked(listProjects).mockReturnValueOnce(new Promise((resolve) => { answer = resolve; }));

    const loading = loadProjects();
    forget();
    answer({ projects: [project('proj-a', 'Alice')], max: 5 });
    await loading;

    expect(projectList.value).toEqual([]);
    expect(projectMax.value).toBe(0);
  });

  it('drops a project created before sign-out instead of listing it for the next account', async () => {
    adopt(alice);
    let answer!: (value: Project) => void;
    vi.mocked(apiCreateProject).mockReturnValueOnce(new Promise((resolve) => { answer = resolve; }));

    const creating = createProject('Alice plans');
    forget();
    answer(project('proj-a', 'Alice plans'));
    await creating;

    expect(projectList.value).toEqual([]);
  });

  it('forgets the picked model and the reasoning setting with the account', () => {
    adopt(alice);
    reasoning.value = { enabled: true, effort: 'high' };
    expect(selectedID.value).toBe('m');

    forget();

    expect(selectedID.value).toBe('');
    expect(models.value).toEqual([]);
    expect(reasoning.value).toEqual({ enabled: false, effort: 'medium' });
  });

  it('drops a model list requested before sign-out instead of pointing the next picker at it', async () => {
    adopt(alice);
    let answer!: (response: Response) => void;
    vi.stubGlobal('fetch', vi.fn(() => new Promise<Response>((resolve) => { answer = resolve; })));

    const loading = loadModels();
    forget();
    answer(new Response(JSON.stringify({ models: [{ id: 'alice-model', display_name: 'Alice model' }] }), {
      status: 200,
      headers: { 'Content-Type': 'application/json' },
    }));
    await loading;

    expect(models.value).toEqual([]);
    expect(selectedID.value).toBe('');
  });

  it('does not keep a model picked for the last account when the next one has no stored default', () => {
    adopt(bob);
    restorePreferences();
    expect(selectedID.value).toBe('');
  });

  it('clears the answer-waiting dot on sign-out, whichever screen the sign-out came from', () => {
    adopt(alice);
    setFeedbackUnread(2);

    forget();

    expect(feedbackUnread.value).toBe(0);
  });

  it('drops a feedback count requested before sign-out instead of showing it to the next account', async () => {
    adopt(alice);
    let answer!: (value: { unread: number }) => void;
    vi.mocked(fetchFeedbackUnread).mockReturnValueOnce(new Promise((resolve) => { answer = resolve; }));

    const refreshing = refreshFeedbackUnread();
    forget();
    answer({ unread: 1 });
    await refreshing;
    expect(feedbackUnread.value).toBe(0);

    // The guard only drops the answer that was asked for the account that left.
    adopt(bob);
    vi.mocked(fetchFeedbackUnread).mockResolvedValueOnce({ unread: 1 });
    await refreshFeedbackUnread();
    expect(feedbackUnread.value).toBe(1);
  });

  it('keeps the next account\'s rail when a clear-all from the last account returns late', async () => {
    adopt(alice);
    conversations.value = [conversation('conv-a', 'Alice only')];
    let finish!: (value: { deleted: number }) => void;
    vi.mocked(deleteAllConversations).mockReturnValueOnce(new Promise((resolve) => { finish = resolve; }));

    const clearing = clearEverything();
    forget();
    adopt(bob);
    vi.mocked(listConversations).mockResolvedValue({ conversations: [conversation('conv-b', 'Bob only')] });
    await refreshList();
    finish({ deleted: 1 });
    await clearing;
    await new Promise((resolve) => { setTimeout(resolve, 0); });

    expect(conversations.value.map((entry) => entry.id)).toEqual(['conv-b']);
  });

  it('reads the rail again when a clear-all overlaps a sign-out and back in to the same account', async () => {
    adopt(alice);
    conversations.value = [conversation('conv-a', 'Alice only')];
    let finish!: (value: { deleted: number }) => void;
    vi.mocked(deleteAllConversations).mockReturnValueOnce(new Promise((resolve) => { finish = resolve; }));

    const clearing = clearEverything();
    forget();
    adopt(alice);
    // Answered before the delete lands, so it still lists what is being deleted.
    vi.mocked(listConversations).mockResolvedValueOnce({ conversations: [conversation('conv-a', 'Alice only')] });
    await refreshList();
    // Created while the delete was in flight, so the delete did not take it.
    vi.mocked(listConversations).mockResolvedValueOnce({ conversations: [conversation('conv-c', 'Alice new')] });
    finish({ deleted: 1 });
    await clearing;
    await new Promise((resolve) => { setTimeout(resolve, 0); });

    expect(conversations.value.map((entry) => entry.id)).toEqual(['conv-c']);
  });

  it('does not flash a clear-all that failed after sign-out into the next account\'s session', async () => {
    adopt(alice);
    conversations.value = [conversation('conv-a', 'Alice only')];
    let fail!: (reason: Error) => void;
    vi.mocked(deleteAllConversations).mockReturnValueOnce(new Promise((_resolve, reject) => { fail = reject; }));

    const clearing = clearEverything();
    forget();
    adopt(bob);
    fail(new Error('the delete timed out'));
    await clearing;

    expect(flash.value).toBe('');
  });

  it('asks again for the priced models after a different administrator signs in', async () => {
    adopt(alice);
    const priced = vi.spyOn(adminApi, 'models').mockResolvedValue({ models: [] });
    await pricedModels();

    forget();
    adopt(bob);
    await pricedModels();

    expect(priced).toHaveBeenCalledTimes(2);
    priced.mockRestore();
  });
});
