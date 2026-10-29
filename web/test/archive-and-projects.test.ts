import { describe, it, expect, beforeEach, afterEach, vi } from 'vitest';
import { createApp, h, ref, nextTick, type App } from 'vue';
import ChatSidebar from '../src/chat/ChatSidebar.vue';
import ChatThinking from '../src/chat/ChatThinking.vue';
import OaPagination from '../src/components/OaPagination.vue';
import OaSelect from '../src/components/OaSelect.vue';
import { providePanelHost } from '../src/composables/usePanelHost';
import { currentUser } from '../src/stores/session';
import { t } from '../src/composables/useI18n';
import * as chatModule from '../src/chat/useChat';
import * as chatApi from '../src/api/chat';

// The rail reads its folders as it mounts. Answered here, so that read never
// reaches a fetch a test has stubbed to count its own requests.
vi.mock('@/api/projects', async (importOriginal) => ({
  ...(await importOriginal<typeof import('../src/api/projects')>()),
  listProjects: vi.fn(async () => ({ projects: [], max: 0 })),
}));

let app: App | null = null;
let host: HTMLElement;

beforeEach(() => {
  document.body.textContent = '';
  host = document.createElement('div');
  document.body.appendChild(host);
  chatModule.conversations.value = [];
  chatModule.railFull.value = false;
  chatModule.activeID.value = '';
  // The menu brings a short list's row into view after its opening transition;
  // jsdom has no layout to scroll, and the timer would fire into a later test.
  if (!Element.prototype.scrollIntoView) Element.prototype.scrollIntoView = () => {};
});

afterEach(() => {
  app?.unmount();
  app = null;
  document.dispatchEvent(new PointerEvent('pointerdown'));
});

// A frame as well as the microtasks: the menus these screens open are OaMenu and OaSelect, which act on an animation frame (see OaMenu.vue and OaSelect.vue).
async function settle(): Promise<void> {
  await Promise.resolve();
  await new Promise((resolve) => requestAnimationFrame(resolve));
  await Promise.resolve();
}

describe('ChatSidebar new layout and 3-dots context menu', () => {
  // The rail holds the latest conversations only and its search filters what
  // it holds, so a full read says the search may be missing older ones.
  for (const [count, shown] of [[chatModule.RAIL_LIMIT, true], [5, false]] as const) {
    it(`${shown ? 'says' : 'does not say'} the search covers only the latest conversations with ${count} listed`, async () => {
      const rows = Array.from({ length: count }, (_, index) => ({
        id: `c${index}`, title: `Chat ${index}`, model_id: 'm', pinned: false, message_count: 1,
        created_at: 0, updated_at: 0,
      }));
      const list = vi.spyOn(chatApi, 'listConversations').mockImplementation(async () => ({ conversations: rows }));
      await chatModule.refreshList();
      list.mockRestore();
      expect(chatModule.conversations.value).toHaveLength(count);
      app = createApp({
        setup() {
          providePanelHost(ref(host));
          return () => h(ChatSidebar);
        },
      });
      app.mount(host);
      await nextTick();
      const note = () => [...host.querySelectorAll('[role="note"]')]
        .some((node) => node.textContent?.trim() === t('searchRecentOnly', { count: chatModule.RAIL_LIMIT }));
      expect(note()).toBe(false);

      const input = host.querySelector<HTMLInputElement>('.oa-history-search input')!;
      input.value = 'Chat';
      input.dispatchEvent(new Event('input'));
      await nextTick();
      expect(note()).toBe(shown);
    });
  }

  it('opens the rail with a new chat row and a new folder beside it', () => {
    app = createApp({
      setup() {
        providePanelHost(ref(host));
        return () => h(ChatSidebar);
      },
    });
    app.mount(host);

    const newBtn = host.querySelector('.ai-history-new-btn');
    expect(newBtn?.classList.contains('ai-chat-new')).toBe(true);
    expect(newBtn?.parentElement?.querySelector('.ai-rail-folder-btn')).not.toBeNull();
    // One list now: the tabs it replaced are gone.
    expect(host.querySelector('[role="tablist"]')).toBeNull();
    // Nothing to search in an empty history, so no field to search it with.
    expect(host.querySelector('.ai-history-search-row')).toBeNull();
    expect(host.querySelector('.ai-chat-list-blank')).not.toBeNull();
  });

  it('renders 3-dots more menu button on conversation item even when canDelete is false', async () => {
    currentUser.value = {
      id: 'u1',
      username: 'test',
      email: '',
      nickname: 'Test',
      avatar: '',
      bio: '',
      role: 'user',
      group_id: 'g1',
      group_expires_at: 0,
      group_name: 'Users',
      status: 'active',
      created_at: 0,
      updated_at: 0,
      last_login_at: 0,
      email_verified: true,
      allow_stats: true,
      allow_delete_conversations: false,
      allow_archive_conversations: true,
      api_restricted: false,
      api_restricted_until: 0,
      api_restriction_source: '',
    };

    chatModule.conversations.value = [
      {
        id: 'c1',
        title: 'Conversation One',
        model_id: 'm1',
        pinned: false,
        message_count: 1,
        created_at: Date.now(),
        updated_at: Date.now(),
      },
    ];

    app = createApp({
      setup() {
        providePanelHost(ref(host));
        return () => h(ChatSidebar);
      },
    });
    app.mount(host);
    await nextTick();

    const item = host.querySelector('.ai-chat-list-item');
    expect(item).not.toBeNull();

    const moreBtn = item?.querySelector<HTMLButtonElement>('.ai-chat-list-more');
    expect(moreBtn).not.toBeNull();

    // Click more options button to open dropdown
    moreBtn?.click();
    await settle();

    // Menu should be open
    const menu = item?.querySelector('.ai-chat-context-menu');
    expect(menu).not.toBeNull();

    // In the menu, rename and archive should be present, but delete must NOT be present
    const menuItems = menu?.querySelectorAll('.oa-menu-item') ?? [];
    const titles = Array.from(menuItems).map((el) => el.textContent?.trim());
    expect(titles.some((t) => t?.includes('Rename') || t?.includes('重命名'))).toBe(true);
    expect(titles.some((t) => t?.includes('Archive') || t?.includes('归档'))).toBe(true);
    expect(titles.some((t) => t?.includes('Delete') || t?.includes('删除'))).toBe(false);
  });

  it('renders delete option in 3-dots menu when canDelete is true', async () => {
    currentUser.value = {
      id: 'u1',
      username: 'test',
      email: '',
      nickname: 'Test',
      avatar: '',
      bio: '',
      role: 'user',
      group_id: 'g1',
      group_expires_at: 0,
      group_name: 'Users',
      status: 'active',
      created_at: 0,
      updated_at: 0,
      last_login_at: 0,
      email_verified: true,
      allow_stats: true,
      allow_delete_conversations: true,
      allow_archive_conversations: true,
      api_restricted: false,
      api_restricted_until: 0,
      api_restriction_source: '',
    };

    chatModule.conversations.value = [
      {
        id: 'c1',
        title: 'Conversation One',
        model_id: 'm1',
        pinned: false,
        message_count: 1,
        created_at: Date.now(),
        updated_at: Date.now(),
      },
    ];

    app = createApp({
      setup() {
        providePanelHost(ref(host));
        return () => h(ChatSidebar);
      },
    });
    app.mount(host);
    await nextTick();

    const moreBtn = host.querySelector<HTMLButtonElement>('.ai-chat-list-more');
    moreBtn?.click();
    await settle();

    const menu = host.querySelector('.ai-chat-context-menu');
    expect(menu).not.toBeNull();

    const menuItems = menu?.querySelectorAll('.oa-menu-item') ?? [];
    const titles = Array.from(menuItems).map((el) => el.textContent?.trim());
    expect(titles.some((t) => t?.includes('Rename') || t?.includes('重命名'))).toBe(true);
    expect(titles.some((t) => t?.includes('Archive') || t?.includes('归档'))).toBe(true);
    expect(titles.some((t) => t?.includes('Delete') || t?.includes('删除'))).toBe(true);
  });
});

describe('ChatThinking animation and structure', () => {
  it('toggles .open class and aria-expanded on click', async () => {
    app = createApp({
      render: () => h(ChatThinking, { text: 'Thought process here', live: false }),
    });
    app.mount(host);

    const root = host.querySelector('.ai-thinking');
    expect(root).not.toBeNull();
    expect(root?.classList.contains('open')).toBe(false);

    const head = host.querySelector<HTMLButtonElement>('.ai-thinking-head');
    expect(head).not.toBeNull();
    expect(head?.getAttribute('aria-expanded')).toBe('false');

    head?.click();
    await nextTick();

    expect(root?.classList.contains('open')).toBe(true);
    expect(head?.getAttribute('aria-expanded')).toBe('true');

    head?.click();
    await nextTick();

    expect(root?.classList.contains('open')).toBe(false);
    expect(head?.getAttribute('aria-expanded')).toBe('false');
  });

  it('starts open when live is true', () => {
    app = createApp({
      render: () => h(ChatThinking, { text: 'Streaming thought', live: true }),
    });
    app.mount(host);

    const root = host.querySelector('.ai-thinking');
    expect(root?.classList.contains('open')).toBe(true);
  });
});

describe('OaPagination mobile keyboard prevention', () => {
  it('configures OaSelect with searchable false', async () => {
    app = createApp({
      render: () => h(OaPagination, {
        page: 1,
        pageSize: 20,
        total: 100,
      }),
    });
    app.mount(host);

    // Clicking trigger opens list without a search input inside
    const trigger = host.querySelector<HTMLButtonElement>('.oa-select');
    expect(trigger).not.toBeNull();
    trigger?.click();
    await settle();

    // The teleported menu should not have .searchable or an input
    const menu = document.body.querySelector('.oa-select-menu');
    expect(menu).not.toBeNull();
    expect(menu?.querySelector('input[type="search"]')).toBeNull();
  });

  it('does not focus search input on coarse pointer (touch device)', async () => {
    const originalMatchMedia = window.matchMedia;
    window.matchMedia = (query: string) => ({
      matches: query === '(pointer: coarse)',
      media: query,
      onchange: null,
      addListener: () => {},
      removeListener: () => {},
      addEventListener: () => {},
      removeEventListener: () => {},
      dispatchEvent: () => false,
    });

    try {
      app = createApp({
        render: () => h(OaSelect, {
          modelValue: 'apple',
          choices: [
            { value: 'apple', label: 'Apple' },
            { value: 'banana', label: 'Banana' },
          ],
          searchable: true,
        }),
      });
      app.mount(host);

      const trigger = host.querySelector<HTMLButtonElement>('.oa-select');
      trigger?.click();
      await settle();

      const menu = document.body.querySelector('.oa-select-menu');
      expect(menu).not.toBeNull();
      const input = menu?.querySelector<HTMLInputElement>('input[type="search"]');
      expect(input).not.toBeNull();
      // On coarse pointer (touch), the search input should NOT have received focus
      expect(document.activeElement).not.toBe(input);
    } finally {
      window.matchMedia = originalMatchMedia;
    }
  });

  // Deleting is permanent and the menu is two clicks away, so the first click
  // only asks. Only the second one reaches the server.
  it('asks once before a conversation is deleted from its menu', async () => {
    const fetchMock = vi.fn(async () => new Response('{}', {
      status: 200,
      headers: { 'Content-Type': 'application/json' },
    }));
    vi.stubGlobal('fetch', fetchMock);
    try {
      chatModule.conversations.value = [
        {
          id: 'c1',
          title: 'Conversation One',
          model_id: 'm1',
          pinned: false,
          message_count: 1,
          created_at: Date.now(),
          updated_at: Date.now(),
        },
      ];

      app = createApp({
        setup() {
          providePanelHost(ref(host));
          return () => h(ChatSidebar);
        },
      });
      app.mount(host);
      await nextTick();

      host.querySelector<HTMLButtonElement>('.ai-chat-list-more')?.click();
      await settle();

      const deleteItem = () => Array.from(host.querySelectorAll<HTMLButtonElement>('.ai-chat-context-menu .oa-menu-item'))
        .find((item) => item.textContent?.includes(t('deleteChat')) || item.textContent?.includes(t('confirmDelete')));
      expect(deleteItem()).toBeDefined();

      deleteItem()!.click();
      await settle();
      expect(fetchMock).not.toHaveBeenCalled();
      expect(deleteItem()?.textContent).toContain(t('confirmDelete'));
      expect(host.querySelector('.ai-chat-context-menu')).not.toBeNull();

      deleteItem()!.click();
      await settle();
      expect(fetchMock).toHaveBeenCalledTimes(1);
      const [url, init] = fetchMock.mock.calls[0] as unknown as [string, RequestInit];
      expect(url).toContain('/api/conversations/c1');
      expect(init.method).toBe('DELETE');
    } finally {
      vi.unstubAllGlobals();
    }
  });
});
