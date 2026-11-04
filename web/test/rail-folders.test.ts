// The rail is one list: folders above, the conversations in none below. A
// conversation is filed by dragging its row onto a folder, taken out by
// dragging it onto the loose list, or moved from its menu without a pointer.
// Nothing on screen moves until the server has said yes, so a refused move
// leaves every list as it was.

import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import { createApp, h, nextTick, ref, type App } from 'vue';
import { createMemoryHistory, createRouter, type Router } from 'vue-router';

import type { Conversation } from '../src/api/chat';
import { ApiError } from '../src/api/client';
import type { Project } from '../src/api/projects';
import { t } from '../src/composables/useI18n';
import { providePanelHost } from '../src/composables/usePanelHost';
import { apiErrorText } from '../src/lib/api-error';

vi.mock('@/api/chat', async (importOriginal) => ({
  ...(await importOriginal<typeof import('../src/api/chat')>()),
  listConversations: vi.fn(),
  getConversation: vi.fn(async (id: string) => ({ conversation: conv(id, titles[id]!, filed[id]), messages: [] })),
  moveConversation: vi.fn(),
  archiveConversation: vi.fn(async (id: string) => { delete filed[id]; return { conversation: conv(id, titles[id]!) }; }),
}));

vi.mock('@/api/projects', async (importOriginal) => ({
  ...(await importOriginal<typeof import('../src/api/projects')>()),
  listProjects: vi.fn(),
  createProject: vi.fn(),
  deleteProject: vi.fn(async () => {}),
}));

const { listConversations, moveConversation: moveApi } = await import('../src/api/chat');
const { createProject: createApi, deleteProject: deleteApi, listProjects } = await import('../src/api/projects');
const ChatSidebar = (await import('../src/chat/ChatSidebar.vue')).default;
const {
  activeID, CONVERSATION_DRAG_TYPE, conversations, dropTarget, flash, moveConversation, railFull, refreshList, resetChat,
} = await import('../src/chat/useChat');
const { createProject, projectList, refreshProjects, resetProjects } = await import('../src/stores/projects');
const { pendingProjectID } = await import('../src/stores/workspace');

function conv(id: string, title: string, projectID = '', updatedAt = 0): Conversation {
  return {
    id, title, model_id: 'm', pinned: false, message_count: 1, created_at: 0, updated_at: updatedAt,
    ...(projectID ? { project_id: projectID } : {}),
  };
}

function folder(id: string, name: string, count: number): Project {
  return { id, name, instructions: '', conversations: count, created_at: 0, updated_at: 0 };
}

const folders = (): Project[] => [folder('p1', 'Folder One', 1), folder('p2', 'Folder Two', 0)];

/** What a browser hands a drag: types readable on every event, the data on the drop. */
class FakeTransfer {
  private data = new Map<string, string>();
  effectAllowed = 'all';
  dropEffect = 'none';
  get types(): string[] { return [...this.data.keys()]; }
  setData(type: string, value: string): void { this.data.set(type, value); }
  getData(type: string): string { return this.data.get(type) ?? ''; }
}

function fire(target: Element, type: string, transfer: FakeTransfer): Event {
  const event = new Event(type, { bubbles: true, cancelable: true });
  Object.defineProperty(event, 'dataTransfer', { value: transfer });
  target.dispatchEvent(event);
  return event;
}

let app: App | null = null;
let host: HTMLElement;
let router: Router;
// The server's filing and titles, which a folder's list is read from and a move changes.
let filed: Record<string, string> = {};
let titles: Record<string, string> = {};

// Microtasks, timers and a frame: the row menu is an OaMenu, which shows on an
// animation frame, and the server's answers are mocks that resolve at once.
async function settle(): Promise<void> {
  for (let i = 0; i < 3; i += 1) {
    await new Promise((resolve) => setTimeout(resolve, 0));
    await nextTick();
  }
  await new Promise((resolve) => requestAnimationFrame(resolve));
  await nextTick();
}

async function mount(path = '/'): Promise<void> {
  router = createRouter({ history: createMemoryHistory(), routes: [{ path: '/:p(.*)*', component: { render: () => null } }] });
  await router.push(path);
  await router.isReady();
  app = createApp({
    setup() {
      providePanelHost(ref(host));
      return () => h(ChatSidebar);
    },
  });
  app.use(router).mount(host);
  await vi.dynamicImportSettled();
  await settle();
}

const looseTitles = () => [...host.querySelectorAll('.ai-rail-loose .ai-chat-list-title')]
  .map((node) => node.textContent?.trim());
const folderBlock = (name: string) => [...host.querySelectorAll<HTMLElement>('.ai-folder')]
  .find((node) => node.querySelector('.ai-folder-open .ai-chat-list-title')?.textContent?.trim() === name)!;
const rowTitled = (scope: ParentNode, title: string) => [...scope.querySelectorAll<HTMLElement>('.ai-chat-list-item')]
  .find((node) => node.querySelector('.ai-chat-list-title')?.textContent?.trim() === title)!;

async function openFolder(name: string): Promise<void> {
  folderBlock(name).querySelector<HTMLButtonElement>('.ai-folder-open')!.click();
  await settle();
}

describe('the rail’s folders', () => {
  beforeEach(() => {
    document.body.textContent = '';
    host = document.createElement('div');
    document.body.appendChild(host);
    if (!Element.prototype.scrollIntoView) Element.prototype.scrollIntoView = () => {};
    // The chat and the folders keep module state between tests; each starts
    // from a signed-out tab, so none passes only because another ran first.
    resetChat();
    resetProjects();
    flash.value = '';
    conversations.value = [conv('c1', 'Loose chat'), conv('c2', 'Filed chat', 'p1')];
    projectList.value = folders();
    vi.mocked(listProjects).mockReset();
    vi.mocked(listProjects).mockImplementation(async () => ({ projects: folders(), max: 0 }));
    // The server's filing, which a folder's list is read from and a move changes.
    filed = { c1: '', c2: 'p1' };
    titles = { c1: 'Loose chat', c2: 'Filed chat' };
    vi.mocked(listConversations).mockReset();
    vi.mocked(listConversations).mockImplementation(async (params) => ({
      conversations: Object.keys(filed)
        .filter((id) => params?.project_id === undefined || filed[id] === params.project_id)
        .map((id) => conv(id, titles[id]!, filed[id])),
    }));
    vi.mocked(deleteApi).mockImplementation(async (projectID) => {
      for (const id of Object.keys(filed)) if (filed[id] === projectID) filed[id] = '';
    });
    vi.mocked(moveApi).mockReset();
    vi.mocked(moveApi).mockImplementation(async (id, projectID) => {
      filed[id] = projectID;
      return { conversation: conv(id, titles[id]!, projectID) };
    });
  });

  afterEach(() => {
    app?.unmount();
    app = null;
    document.dispatchEvent(new PointerEvent('pointerdown'));
    vi.restoreAllMocks();
  });

  it('draws the folders above and only the conversations in none below', async () => {
    await mount();

    expect(host.querySelector('[role="tablist"]')).toBeNull();
    expect([...host.querySelectorAll('.ai-folder-open .ai-chat-list-title')].map((node) => node.textContent?.trim()))
      .toEqual(['Folder One', 'Folder Two']);
    expect(folderBlock('Folder One').querySelector('.ai-folder-count')?.textContent?.trim()).toBe('1');
    expect(looseTitles()).toEqual(['Loose chat']);
    // The filed one is under its folder, once, and not among the loose ones.
    await openFolder('Folder One');
    expect(rowTitled(folderBlock('Folder One'), 'Filed chat')).toBeTruthy();
    expect(host.querySelectorAll('.ai-chat-list-title')).toHaveLength(4);
  });

  it('files a row dropped on a folder, and takes it out of the loose list', async () => {
    await mount();
    const transfer = new FakeTransfer();
    fire(rowTitled(host.querySelector('.ai-rail-loose')!, 'Loose chat'), 'dragstart', transfer);
    expect(transfer.types).toEqual([CONVERSATION_DRAG_TYPE]);

    const target = folderBlock('Folder Two');
    expect(fire(target, 'dragover', transfer).defaultPrevented).toBe(true);
    await nextTick();
    expect(target.classList.contains('drop-target')).toBe(true);

    vi.mocked(listProjects).mockImplementationOnce(async () => ({
      projects: [folder('p1', 'Folder One', 1), folder('p2', 'Folder Two', 1)], max: 0,
    }));
    fire(target, 'drop', transfer);
    await settle();

    expect(moveApi).toHaveBeenCalledWith('c1', 'p2');
    expect(looseTitles()).toEqual([]);
    expect(conversations.value.find((entry) => entry.id === 'c1')?.project_id).toBe('p2');
    // The counts are read back from the server rather than worked out here.
    expect(folderBlock('Folder Two').querySelector('.ai-folder-count')?.textContent?.trim()).toBe('1');
    expect(target.classList.contains('drop-target')).toBe(false);
  });

  it('takes a filed row out of its folder when it is dropped on the loose list', async () => {
    await mount();
    await openFolder('Folder One');
    const transfer = new FakeTransfer();
    fire(rowTitled(folderBlock('Folder One'), 'Filed chat'), 'dragstart', transfer);

    const loose = host.querySelector('.ai-rail-loose')!;
    expect(fire(loose, 'dragover', transfer).defaultPrevented).toBe(true);
    fire(loose, 'drop', transfer);
    await settle();

    expect(moveApi).toHaveBeenCalledWith('c2', '');
    expect(looseTitles()).toEqual(['Loose chat', 'Filed chat']);
    expect(rowTitled(folderBlock('Folder One'), 'Filed chat')).toBeUndefined();
  });

  it('does nothing with a row dropped on the folder it is already in', async () => {
    await mount();
    await openFolder('Folder One');
    const transfer = new FakeTransfer();
    fire(rowTitled(folderBlock('Folder One'), 'Filed chat'), 'dragstart', transfer);

    const own = folderBlock('Folder One');
    expect(fire(own, 'dragover', transfer).defaultPrevented).toBe(false);
    await nextTick();
    expect(own.classList.contains('drop-target')).toBe(false);
    fire(own, 'drop', transfer);
    await settle();

    expect(moveApi).not.toHaveBeenCalled();
  });

  it('leaves everything where it was when the server refuses, and says why', async () => {
    const refusal = new ApiError(404, 'not_found', 'No such project.');
    vi.mocked(moveApi).mockRejectedValueOnce(refusal);
    await mount();
    // The rail reads its folders as it mounts; only what the move does counts.
    vi.mocked(listProjects).mockClear();
    const transfer = new FakeTransfer();
    fire(rowTitled(host.querySelector('.ai-rail-loose')!, 'Loose chat'), 'dragstart', transfer);
    fire(folderBlock('Folder Two'), 'dragover', transfer);
    fire(folderBlock('Folder Two'), 'drop', transfer);
    await settle();

    expect(moveApi).toHaveBeenCalledWith('c1', 'p2');
    expect(flash.value).toBe(apiErrorText(refusal));
    expect(looseTitles()).toEqual(['Loose chat']);
    expect(conversations.value.find((entry) => entry.id === 'c1')?.project_id).toBeUndefined();
    // Nothing moved, so there is nothing to count again.
    expect(listProjects).not.toHaveBeenCalled();
  });

  it('moves a row from its menu, without a drag', async () => {
    await mount();
    rowTitled(host.querySelector('.ai-rail-loose')!, 'Loose chat')
      .querySelector<HTMLButtonElement>('.ai-chat-list-more')!.click();
    await settle();

    const items = () => [...host.querySelectorAll<HTMLButtonElement>('.ai-chat-context-menu .oa-menu-item')];
    const titled = (title: string) => items().find((item) => item.textContent?.trim() === title);
    // Loose, so there is nothing to take it out of.
    expect(titled(t('moveOutOfFolder'))).toBeUndefined();
    expect(host.querySelector('.ai-chat-menu-folder')).toBeNull();

    titled(t('moveToFolder'))!.click();
    await settle();
    expect([...host.querySelectorAll('.ai-chat-menu-folder')].map((node) => node.textContent?.trim()))
      .toEqual(['Folder One', 'Folder Two']);

    titled('Folder Two')!.click();
    await settle();
    expect(moveApi).toHaveBeenCalledWith('c1', 'p2');
    expect(looseTitles()).toEqual([]);
  });

  it('offers a filed row out of its folder, and only the other folders to go to', async () => {
    await mount();
    await openFolder('Folder One');
    rowTitled(folderBlock('Folder One'), 'Filed chat')
      .querySelector<HTMLButtonElement>('.ai-chat-list-more')!.click();
    await settle();

    const items = () => [...host.querySelectorAll<HTMLButtonElement>('.ai-chat-context-menu .oa-menu-item')];
    const titled = (title: string) => items().find((item) => item.textContent?.trim() === title);
    titled(t('moveToFolder'))!.click();
    await settle();
    expect([...host.querySelectorAll('.ai-chat-menu-folder')].map((node) => node.textContent?.trim()))
      .toEqual(['Folder Two']);

    titled(t('moveOutOfFolder'))!.click();
    await settle();
    expect(moveApi).toHaveBeenCalledWith('c2', '');
    expect(looseTitles()).toEqual(['Loose chat', 'Filed chat']);
  });

  it('does not let a folder read begun before a move put the moved row back', async () => {
    // Too old for the rail: nothing the rail holds changes when it moves, so
    // nothing else reads its old folder again after the move.
    conversations.value = [conv('c1', 'Loose chat')];
    await mount();
    await openFolder('Folder One');
    // A turn ends and the open folder is read again; that read is slow.
    let answerStale: (value: { conversations: Conversation[] }) => void = () => {};
    vi.mocked(listConversations).mockImplementationOnce(() => new Promise((resolve) => { answerStale = resolve; }));
    conversations.value = [...conversations.value];
    await settle();

    const transfer = new FakeTransfer();
    fire(rowTitled(folderBlock('Folder One'), 'Filed chat'), 'dragstart', transfer);
    fire(folderBlock('Folder Two'), 'dragover', transfer);
    fire(folderBlock('Folder Two'), 'drop', transfer);
    await settle();
    expect(moveApi).toHaveBeenCalledWith('c2', 'p2');
    expect(rowTitled(folderBlock('Folder One'), 'Filed chat')).toBeUndefined();

    // The read answers with the folder as it was before the move.
    answerStale({ conversations: [conv('c2', 'Filed chat', 'p1')] });
    await settle();
    expect(rowTitled(folderBlock('Folder One'), 'Filed chat')).toBeUndefined();
  });

  it('still cancels a file dropped on the rail, and lets a row’s drag through', async () => {
    await mount();
    const rail = host.querySelector('.ai-chat-sidebar')!;
    const files = new FakeTransfer();
    files.setData('Files', '');
    const fileOver = fire(rail, 'dragover', files);
    expect(fileOver.defaultPrevented).toBe(true);
    expect(files.dropEffect).toBe('none');

    // Off every target, a row's drag is neither taken nor refused by the rail.
    const rowDrag = new FakeTransfer();
    fire(rowTitled(host.querySelector('.ai-rail-loose')!, 'Loose chat'), 'dragstart', rowDrag);
    expect(fire(host.querySelector('.ai-chat-sidebar-head')!, 'dragover', rowDrag).defaultPrevented).toBe(false);
    fire(rowTitled(host.querySelector('.ai-rail-loose')!, 'Loose chat'), 'dragend', rowDrag);
  });

  it('lists the loose conversations even when the newest of every kind are all filed', async () => {
    // Sixty newer than any loose one, every one of them in a folder.
    const newest = Array.from({ length: 60 }, (_, index) => conv(`f${index}`, `Filed ${index}`, 'p1', 1000 - index));
    const loose = [conv('old1', 'Old loose', '', 10), conv('old2', 'Older loose', '', 5)];
    vi.mocked(listConversations).mockImplementation(async (params) => ({
      conversations: params?.project_id === '' ? loose : newest,
    }));
    await refreshList();
    expect(listConversations).toHaveBeenCalledWith({ limit: 60 });
    expect(listConversations).toHaveBeenCalledWith({ limit: 60, project_id: '' });
    // One list, each conversation once, in the server's order.
    expect(conversations.value).toHaveLength(62);
    expect(conversations.value.slice(-2).map((entry) => entry.id)).toEqual(['old1', 'old2']);
    expect(railFull.value).toBe(true);

    await mount();
    expect(looseTitles()).toEqual(['Old loose', 'Older loose']);

    // The search finds them too, and says it may be missing older ones.
    const input = host.querySelector<HTMLInputElement>('.oa-history-search input')!;
    input.value = 'Older';
    input.dispatchEvent(new Event('input'));
    await settle();
    expect([...host.querySelectorAll('.ai-chat-list-title')].map((node) => node.textContent?.trim()))
      .toContain('Older loose');
    expect(host.querySelector('[role="note"]')?.textContent?.trim()).toBe(t('searchRecentOnly', { count: 60 }));
  });

  it('says nothing about older conversations when neither read came back full', async () => {
    vi.mocked(listConversations).mockImplementation(async (params) => ({
      conversations: params?.project_id === ''
        ? [conv('c1', 'Loose chat', '', 2)]
        : [conv('c1', 'Loose chat', '', 2), conv('c2', 'Filed chat', 'p1', 1)],
    }));
    await refreshList();
    expect(conversations.value.map((entry) => entry.id)).toEqual(['c1', 'c2']);
    expect(railFull.value).toBe(false);
  });

  it('lights nothing over the folder a row is already in, even after lighting another', async () => {
    await mount();
    await openFolder('Folder One');
    const transfer = new FakeTransfer();
    fire(rowTitled(folderBlock('Folder One'), 'Filed chat'), 'dragstart', transfer);

    const loose = host.querySelector('.ai-rail-loose')!;
    fire(loose, 'dragover', transfer);
    await nextTick();
    expect(loose.classList.contains('drop-target')).toBe(true);

    expect(fire(folderBlock('Folder One'), 'dragover', transfer).defaultPrevented).toBe(false);
    await nextTick();
    expect(dropTarget.value).toBeNull();
    expect(loose.classList.contains('drop-target')).toBe(false);
  });

  it('leaves a read of a folder the moved row was never in to land', async () => {
    projectList.value = [...folders(), folder('p3', 'Folder Three', 0)];
    vi.mocked(listProjects).mockImplementation(async () => ({ projects: projectList.value, max: 0 }));
    // Too old for the rail, so its move changes nothing the rail holds.
    conversations.value = [conv('c1', 'Loose chat')];
    filed.c9 = 'p2';
    titles.c9 = 'Old filed';
    await mount();
    await openFolder('Folder One');
    await openFolder('Folder Two');

    // A turn ends and both open folders are read again; Folder One's is slow,
    // and answers with a conversation the server has filed there since.
    let answerOne: (value: { conversations: Conversation[] }) => void = () => {};
    vi.mocked(listConversations).mockImplementationOnce(() => new Promise((resolve) => { answerOne = resolve; }));
    conversations.value = [...conversations.value];
    await settle();

    const transfer = new FakeTransfer();
    fire(rowTitled(folderBlock('Folder Two'), 'Old filed'), 'dragstart', transfer);
    fire(folderBlock('Folder Three'), 'dragover', transfer);
    fire(folderBlock('Folder Three'), 'drop', transfer);
    await settle();
    expect(moveApi).toHaveBeenCalledWith('c9', 'p3');
    expect(rowTitled(folderBlock('Folder Two'), 'Old filed')).toBeUndefined();

    answerOne({ conversations: [conv('c2', 'Filed chat', 'p1'), conv('c3', 'New in one', 'p1')] });
    await settle();
    expect(rowTitled(folderBlock('Folder One'), 'New in one')).toBeTruthy();
  });

  it('puts a conversation too old for the rail, moved out of a folder, where the server would list it', async () => {
    conversations.value = [conv('a', 'Newer', '', 300), conv('b', 'Older', '', 100)];
    vi.mocked(moveApi).mockImplementationOnce(async (id) => ({ conversation: conv(id, 'Middle', '', 200) }));
    expect(await moveConversation('m', '', 'p1')).toBe(true);
    expect(conversations.value.map((entry) => entry.id)).toEqual(['a', 'm', 'b']);

    // Older than everything held, and the loose read was not full: it is the last loose one.
    vi.mocked(moveApi).mockImplementationOnce(async (id) => ({ conversation: conv(id, 'Oldest', '', 1) }));
    expect(await moveConversation('z', '', 'p1')).toBe(true);
    expect(conversations.value.map((entry) => entry.id)).toEqual(['a', 'm', 'b', 'z']);
  });

  it('leaves out a conversation moved out of a folder that is older than a full loose read', async () => {
    const loose = Array.from({ length: 60 }, (_, index) => conv(`l${index}`, `Loose ${index}`, '', 1000 - index));
    vi.mocked(listConversations).mockImplementation(async () => ({ conversations: loose }));
    await refreshList();
    vi.mocked(moveApi).mockImplementationOnce(async (id) => ({ conversation: conv(id, 'Ancient', '', 1) }));

    expect(await moveConversation('old', '', 'p1')).toBe(true);
    // Read again, the rail would not hold it either.
    expect(conversations.value.some((entry) => entry.id === 'old')).toBe(false);
    expect(conversations.value).toHaveLength(60);
  });

  it('points the composer at the folder the open conversation was moved to', async () => {
    activeID.value = 'c1';
    pendingProjectID.value = '';
    expect(await moveConversation('c1', 'p2')).toBe(true);
    expect(pendingProjectID.value).toBe('p2');
    expect(await moveConversation('c1', '')).toBe(true);
    expect(pendingProjectID.value).toBe('');
    // A conversation that is not the open one leaves the composer where it was.
    pendingProjectID.value = 'p1';
    expect(await moveConversation('c2', 'p2')).toBe(true);
    expect(pendingProjectID.value).toBe('p1');
  });

  it('releases a deleted folder’s conversations into the loose list', async () => {
    await mount();
    expect(looseTitles()).toEqual(['Loose chat']);
    const remove = folderBlock('Folder One').querySelector<HTMLButtonElement>('.ai-project-action-btn.danger')!;
    remove.click(); // the first click arms the confirmation
    await settle();
    remove.click();
    await settle();

    expect(host.querySelectorAll('.ai-folder')).toHaveLength(1);
    expect(conversations.value.find((entry) => entry.id === 'c2')?.project_id || '').toBe('');
    expect(looseTitles()).toEqual(['Filed chat', 'Loose chat']);
  });

  it('keeps the folder count when a conversation is archived, as the server does', async () => {
    await mount();
    await openFolder('Folder One');
    rowTitled(folderBlock('Folder One'), 'Filed chat').querySelector<HTMLButtonElement>('.ai-chat-list-more')!.click();
    await settle();
    [...host.querySelectorAll<HTMLButtonElement>('.ai-chat-context-menu .oa-menu-item')]
      .find((item) => item.textContent?.trim() === t('archive'))!.click();
    await settle();
    expect(rowTitled(folderBlock('Folder One'), 'Filed chat')).toBeUndefined();
    expect(folderBlock('Folder One').querySelector('.ai-folder-count')?.textContent?.trim()).toBe('1');
  });

  it('loads uncached conversations released by deleting a folder', async () => {
    conversations.value = [conv('c1', 'Loose chat')];
    filed.old = 'p1';
    titles.old = 'Old uncached';
    await mount();
    const remove = folderBlock('Folder One').querySelector<HTMLButtonElement>('.ai-project-action-btn.danger')!;
    remove.click();
    await settle();
    remove.click();
    await settle();
    expect(looseTitles()).toContain('Old uncached');
  });

  it('does not let a rail refresh begun before a move restore its old folder', async () => {
    const answers: Array<(value: { conversations: Conversation[] }) => void> = [];
    vi.mocked(listConversations).mockImplementation(() => new Promise((resolve) => { answers.push(resolve); }));
    const reading = refreshList();
    expect(await moveConversation('c1', 'p2')).toBe(true);
    for (const answer of answers) answer({ conversations: [conv('c1', 'Loose chat')] });
    await reading;
    expect(conversations.value.find((entry) => entry.id === 'c1')?.project_id).toBe('p2');
  });

  it('keeps the newest rail refresh when an older refresh answers last', async () => {
    const answers: Array<(value: { conversations: Conversation[] }) => void> = [];
    vi.mocked(listConversations).mockImplementation(() => new Promise((resolve) => { answers.push(resolve); }));
    const first = refreshList();
    const second = refreshList();
    answers[2]!({ conversations: [conv('c1', 'New title')] });
    answers[3]!({ conversations: [conv('c1', 'New title')] });
    await second;
    answers[0]!({ conversations: [conv('c1', 'Old title')] });
    answers[1]!({ conversations: [conv('c1', 'Old title')] });
    await first;
    expect(conversations.value.find((entry) => entry.id === 'c1')?.title).toBe('New title');
  });

  it('returns from the terminal when a filed conversation is opened or started', async () => {
    await mount('/terminal');
    await openFolder('Folder One');
    rowTitled(folderBlock('Folder One'), 'Filed chat').querySelector<HTMLButtonElement>('.ai-chat-list-open')!.click();
    await settle();
    expect(router.currentRoute.value.path).toBe('/');
    expect(activeID.value).toBe('c2');

    await router.push('/terminal');
    folderBlock('Folder One').querySelector<HTMLButtonElement>('.ai-project-new-chat')!.click();
    await settle();
    expect(router.currentRoute.value.path).toBe('/');
    expect(activeID.value).toBe('');
    expect(pendingProjectID.value).toBe('p1');
  });

  it('names a folder with its count, and hides the bare figure from a screen reader', async () => {
    await mount();
    const block = folderBlock('Folder One');
    expect(block.querySelector('.ai-folder-open')?.getAttribute('aria-label'))
      .toBe(t('folderSummary', { name: 'Folder One', count: 1 }));
    expect(block.querySelector('.ai-folder-count')?.getAttribute('aria-hidden')).toBe('true');
  });

  it('opens and focuses the first-folder form after its content loads', async () => {
    projectList.value = [];
    vi.mocked(listProjects).mockResolvedValue({ projects: [], max: 0 });
    await mount();
    expect(host.querySelector('.ai-folder-section')).toBeNull();
    host.querySelector<HTMLButtonElement>('.ai-rail-folder-btn')!.click();
    await vi.dynamicImportSettled();
    await settle();
    const form = host.querySelector('.ai-project-form');
    expect(form).not.toBeNull();
    expect(form!.contains(document.activeElement)).toBe(true);
  });

  it('ends a search to start a folder, so the form it opens is on screen and focused', async () => {
    await mount();
    const input = host.querySelector<HTMLInputElement>('.oa-history-search input')!;
    input.value = 'Loose';
    input.dispatchEvent(new Event('input'));
    await settle();
    const section = host.querySelector<HTMLElement>('.ai-folder-section')!;
    expect(section.style.display).toBe('none');

    host.querySelector<HTMLButtonElement>('.ai-rail-folder-btn')!.click();
    await settle();

    expect(input.value).toBe('');
    expect(section.style.display).not.toBe('none');
    const form = section.querySelector('.ai-project-form');
    expect(form).not.toBeNull();
    expect(form!.contains(document.activeElement)).toBe(true);
    expect(document.activeElement?.tagName).toBe('INPUT');
  });

  it('unfolds the folders under the menu item, in the menu’s title column, on a list that scrolls by itself', async () => {
    await mount();
    rowTitled(host.querySelector('.ai-rail-loose')!, 'Loose chat')
      .querySelector<HTMLButtonElement>('.ai-chat-list-more')!.click();
    await settle();
    const trigger = [...host.querySelectorAll<HTMLButtonElement>('.ai-chat-context-menu .oa-menu-item')]
      .find((item) => item.textContent?.trim() === t('moveToFolder'))!;
    expect(trigger.classList.contains('active')).toBe(false);
    trigger.click();
    await settle();

    expect(trigger.classList.contains('active')).toBe(true);
    expect(trigger.getAttribute('aria-expanded')).toBe('true');
    expect(trigger.querySelector('.ai-chat-menu-chevron.expanded')).not.toBeNull();
    const choices = [...host.querySelectorAll<HTMLElement>('.ai-chat-menu-folder')];
    expect(choices).toHaveLength(2);
    for (const choice of choices) {
      // A leading glyph, as every sibling has, so the name starts in their column.
      expect(choice.querySelector('.oa-menu-row > svg.ai-chat-menu-folder-glyph')).not.toBeNull();
      expect(choice.closest('.ai-chat-menu-folders-wrap .ai-chat-menu-folders')).not.toBeNull();
    }
  });

  it('keeps focus after a move from the menu, and says where the row went', async () => {
    await mount();
    rowTitled(host.querySelector('.ai-rail-loose')!, 'Loose chat')
      .querySelector<HTMLButtonElement>('.ai-chat-list-more')!.click();
    await settle();
    const items = () => [...host.querySelectorAll<HTMLButtonElement>('.ai-chat-context-menu .oa-menu-item')];
    items().find((item) => item.textContent?.trim() === t('moveToFolder'))!.click();
    await settle();
    items().find((item) => item.textContent?.trim() === 'Folder Two')!.click();
    await settle();

    expect(flash.value).toBe(t('movedToFolder', { name: 'Folder Two' }));
    // Folder Two is closed, so the row is drawn nowhere; its folder takes focus.
    expect(document.activeElement).toBe(folderBlock('Folder Two').querySelector('.ai-folder-open'));
  });

  it('gives focus to the row in its new place when it is drawn there', async () => {
    await mount();
    await openFolder('Folder One');
    (document.activeElement as HTMLElement | null)?.blur();
    rowTitled(folderBlock('Folder One'), 'Filed chat')
      .querySelector<HTMLButtonElement>('.ai-chat-list-more')!.click();
    await settle();
    [...host.querySelectorAll<HTMLButtonElement>('.ai-chat-context-menu .oa-menu-item')]
      .find((item) => item.textContent?.trim() === t('moveOutOfFolder'))!.click();
    await settle();

    expect(flash.value).toBe(t('movedOutOfFolder'));
    expect(document.activeElement).toBe(
      rowTitled(host.querySelector('.ai-rail-loose')!, 'Filed chat').querySelector('.ai-chat-list-open'),
    );
  });
});

describe('the folders store', () => {
  beforeEach(() => {
    resetProjects();
    vi.mocked(listProjects).mockReset();
    vi.mocked(createApi).mockReset();
  });

  it('keeps the newest refresh when an older one answers last', async () => {
    const answers: Array<(value: { projects: Project[]; max: number }) => void> = [];
    vi.mocked(listProjects).mockImplementation(() => new Promise((resolve) => { answers.push(resolve); }));
    const first = refreshProjects();
    const second = refreshProjects();
    answers[1]!({ projects: [folder('p1', 'Folder One', 2)], max: 0 });
    await second;
    answers[0]!({ projects: [folder('p1', 'Folder One', 1)], max: 0 });
    await first;

    expect(projectList.value.map((project) => project.conversations)).toEqual([2]);
  });

  it('does not let a read begun before a folder was made answer without it', async () => {
    let answer: (value: { projects: Project[]; max: number }) => void = () => {};
    vi.mocked(listProjects).mockImplementation(() => new Promise((resolve) => { answer = resolve; }));
    vi.mocked(createApi).mockResolvedValue(folder('p9', 'Made now', 0));
    const reading = refreshProjects();
    await createProject('Made now');
    answer({ projects: [folder('p1', 'Folder One', 1)], max: 0 });
    await reading;

    expect(projectList.value.map((project) => project.id)).toEqual(['p9']);
  });
});
