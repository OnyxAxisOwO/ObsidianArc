// The archive panel as a reader meets it: restoring and deleting archived
// conversations, and reaching the ones past the first page.
//
// A restore the server refused must leave the row where it was, because the
// conversation is still archived. And the page after a restore has to start one
// row earlier, or the row that moved up into the gap is skipped for good.

import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import { createApp, h, nextTick, ref, type App } from 'vue';
import { createMemoryHistory, createRouter } from 'vue-router';
import * as chatApi from '@/api/chat';
import type { Conversation, Message } from '@/api/chat';
import { ApiError } from '@/api/client';
import * as chat from '@/chat/useChat';
import { changeLanguage, t } from '@/composables/useI18n';
import { providePanelHost } from '@/composables/usePanelHost';
import { forget } from '@/stores/session';
import ArchivePanel from '@/views/ArchivePanel.vue';

let app: App | null = null;
let host: HTMLElement;
let panelHost: HTMLElement;
// What the fake server holds as archived, in the order it lists them.
let rows: Conversation[] = [];

function row(index: number): Conversation {
  return {
    id: `c${String(index).padStart(3, '0')}`,
    title: `Chat ${index}`,
    model_id: 'm1',
    mode: 'chat',
    pinned: false,
    archived: true,
    message_count: 1,
    created_at: 0,
    updated_at: 100000 - index,
  } as Conversation;
}

function makeRows(count: number): Conversation[] {
  return Array.from({ length: count }, (_, index) => row(index));
}

/** The server's page for a request, as the archive panel asks for it. */
function serve(params?: chatApi.ListConversationsParams): Promise<{ conversations: Conversation[] }> {
  const offset = params?.offset ?? 0;
  const limit = params?.limit ?? 20;
  return Promise.resolve({ conversations: rows.slice(offset, offset + limit) });
}

function archiveRows(): HTMLElement[] {
  return [...panelHost.querySelectorAll<HTMLElement>('.oa-archive-row')];
}

function loadMoreButton(): HTMLButtonElement | null {
  return panelHost.querySelector<HTMLButtonElement>('.oa-archive-load-more button');
}

function restoreButton(rowElement: HTMLElement): HTMLButtonElement {
  return rowElement.querySelector<HTMLButtonElement>(`button[aria-label="${t('unarchive')}"]`)!;
}

function deleteButton(rowElement: HTMLElement): HTMLButtonElement {
  return rowElement.querySelector<HTMLButtonElement>('button.danger')!;
}

async function settle(): Promise<void> {
  for (let i = 0; i < 3; i++) {
    await new Promise((resolve) => setTimeout(resolve, 0));
    await nextTick();
  }
}

async function mountPanel(): Promise<void> {
  const router = createRouter({
    history: createMemoryHistory(),
    routes: [{ path: '/:p(.*)*', component: { render: () => null } }],
  });
  await router.push('/archive');
  await router.isReady();
  app = createApp({
    setup() {
      providePanelHost(ref(panelHost));
      return () => h(ArchivePanel);
    },
  });
  app.use(router).mount(host);
  await settle();
}

beforeEach(async () => {
  await changeLanguage('en');
  host = document.createElement('div');
  panelHost = document.createElement('div');
  document.body.append(host, panelHost);
  rows = [];
  chat.activeID.value = '';
  chat.messages.value = [];
  chat.flash.value = '';
});

afterEach(() => {
  app?.unmount();
  app = null;
  forget();
  document.body.textContent = '';
  vi.restoreAllMocks();
});

describe('the archive panel', () => {
  it('lists the next page after the rows already shown, and leaves none out', async () => {
    rows = makeRows(102);
    const list = vi.spyOn(chatApi, 'listConversations').mockImplementation(serve);
    await mountPanel();

    expect(list).toHaveBeenCalledWith({ archived: true, limit: 100, offset: 0 });
    expect(archiveRows()).toHaveLength(100);
    expect(loadMoreButton()).not.toBeNull();

    loadMoreButton()!.click();
    await settle();

    expect(list).toHaveBeenLastCalledWith({ archived: true, limit: 100, offset: 100 });
    expect(archiveRows()).toHaveLength(102);
    expect(loadMoreButton()).toBeNull();
  });

  it('starts the next page where the list stands after a restore, so no row is skipped', async () => {
    rows = makeRows(102);
    vi.spyOn(chatApi, 'archiveConversation').mockImplementation(async (id: string) => {
      rows = rows.filter((entry) => entry.id !== id);
      return { conversation: { id } as Conversation };
    });
    const list = vi.spyOn(chatApi, 'listConversations').mockImplementation(serve);
    await mountPanel();

    restoreButton(archiveRows()[0]!).click();
    await settle();
    expect(archiveRows()).toHaveLength(99);

    loadMoreButton()!.click();
    await settle();

    // One row has left the archive, so the page starts at 99, not 100.
    expect(list).toHaveBeenLastCalledWith({ archived: true, limit: 100, offset: 99 });
    const ids = archiveRows().map((element) => element.querySelector('.oa-card-title')?.textContent);
    expect(new Set(ids).size).toBe(101);
    expect(ids).toContain('Chat 101');
  });

  it('does not page while a restore is still pending, so the page is not cut from a shifted list', async () => {
    rows = makeRows(102);
    let finishRestore!: () => void;
    vi.spyOn(chatApi, 'archiveConversation').mockImplementation(async (id: string) => {
      // The server applies the change when it receives it, so a page answered
      // after it is cut from the shifted list.
      rows = rows.filter((entry) => entry.id !== id);
      await new Promise<void>((resolve) => { finishRestore = resolve; });
      return { conversation: { id } as Conversation };
    });
    const list = vi.spyOn(chatApi, 'listConversations').mockImplementation(serve);
    await mountPanel();

    restoreButton(archiveRows()[0]!).click();
    await settle();
    expect(loadMoreButton()!.disabled).toBe(true);

    loadMoreButton()!.click();
    await settle();
    expect(list).toHaveBeenCalledTimes(1);

    finishRestore();
    await settle();
    loadMoreButton()!.click();
    await settle();

    expect(list).toHaveBeenLastCalledWith({ archived: true, limit: 100, offset: 99 });
    expect(archiveRows()).toHaveLength(101);
  });

  it('does not restore a row while the next page is loading, so the page is not cut from a shifted list', async () => {
    rows = makeRows(102);
    const archive = vi.spyOn(chatApi, 'archiveConversation').mockResolvedValue({ conversation: {} as Conversation });
    let finishPage!: () => void;
    vi.spyOn(chatApi, 'listConversations').mockImplementation(async (params) => {
      if (params?.offset === 100) await new Promise<void>((resolve) => { finishPage = resolve; });
      return serve(params);
    });
    await mountPanel();

    loadMoreButton()!.click();
    await settle();
    restoreButton(archiveRows()[0]!).click();
    await settle();
    expect(archive).not.toHaveBeenCalled();

    finishPage();
    await settle();
    expect(archiveRows()).toHaveLength(102);
  });

  it('leaves a conversation listed when restoring it fails, since it is still archived', async () => {
    rows = makeRows(3);
    vi.spyOn(chatApi, 'listConversations').mockImplementation(serve);
    vi.spyOn(chatApi, 'archiveConversation').mockRejectedValue(
      new ApiError(500, 'internal', 'The archive is unavailable.', {}),
    );
    await mountPanel();

    restoreButton(archiveRows()[0]!).click();
    await settle();

    expect(archiveRows()).toHaveLength(3);
    // A 500 is not shown in the server's English, so the reader gets the chat's
    // own fallback line; see apiErrorText.
    expect(chat.flash.value).toBe(t('failed'));
  });

  it('clears the open transcript when the conversation shown in it is deleted from the archive', async () => {
    rows = makeRows(2);
    const open = rows[0]!.id;
    vi.spyOn(chatApi, 'deleteConversation').mockImplementation(async (id: string) => {
      rows = rows.filter((entry) => entry.id !== id);
    });
    vi.spyOn(chatApi, 'listConversations').mockImplementation(serve);
    chat.activeID.value = open;
    chat.messages.value = [{ id: 'old', role: 'user', content: 'from the deleted chat' } as Message];
    await mountPanel();

    const remove = deleteButton(archiveRows()[0]!);
    remove.click(); // the first click arms the confirmation
    await settle();
    // Armed, the button shows the check glyph. Without the icon imported it
    // renders as an unknown element with no drawing.
    expect(remove.classList.contains('armed')).toBe(true);
    expect(remove.querySelector('svg')).not.toBeNull();

    remove.click();
    await settle();

    expect(archiveRows()).toHaveLength(1);
    expect(chat.activeID.value).toBe('');
    expect(chat.messages.value).toEqual([]);
  });

  it('shows the reader line for a coded refusal of a delete, not the server sentence', async () => {
    rows = makeRows(2);
    vi.spyOn(chatApi, 'listConversations').mockImplementation(serve);
    vi.spyOn(chatApi, 'deleteConversation').mockRejectedValue(
      new ApiError(400, 'storage_full', 'The server sentence, in English.', {}),
    );
    await mountPanel();

    const remove = deleteButton(archiveRows()[0]!);
    remove.click(); // the first click arms the confirmation
    await settle();
    remove.click();
    await settle();

    expect(archiveRows()).toHaveLength(2);
    expect(chat.flash.value).toBe(t('errStorageFull'));
    expect(chat.flash.value).not.toBe('The server sentence, in English.');
  });
});
