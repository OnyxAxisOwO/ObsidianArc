import { describe, it, expect, beforeEach, afterEach } from 'vitest';
import { createApp, h, ref, nextTick, type App } from 'vue';
import ChatSidebar from '../src/chat/ChatSidebar.vue';
import ChatThinking from '../src/chat/ChatThinking.vue';
import OaPagination from '../src/components/OaPagination.vue';
import OaSelect from '../src/components/OaSelect.vue';
import { providePanelHost } from '../src/composables/usePanelHost';
import { currentUser } from '../src/stores/session';
import * as chatModule from '../src/chat/useChat';

let app: App | null = null;
let host: HTMLElement;

beforeEach(() => {
  document.body.textContent = '';
  host = document.createElement('div');
  document.body.appendChild(host);
  chatModule.conversations.value = [];
  chatModule.activeID.value = '';
});

afterEach(() => {
  app?.unmount();
  app = null;
  document.dispatchEvent(new PointerEvent('pointerdown'));
  document.body.textContent = '';
});

async function settle(): Promise<void> {
  await Promise.resolve();
  await new Promise((resolve) => requestAnimationFrame(resolve));
  await Promise.resolve();
}

describe('ChatSidebar new layout and 3-dots context menu', () => {
  it('renders a square new chat button beside the search field', () => {
    app = createApp({
      setup() {
        providePanelHost(ref(host));
        return () => h(ChatSidebar);
      },
    });
    app.mount(host);

    const searchRow = host.querySelector('.ai-history-search-row');
    expect(searchRow).not.toBeNull();
    const searchField = searchRow?.querySelector('.oa-history-search');
    expect(searchField).not.toBeNull();
    const newBtn = searchRow?.querySelector('.ai-history-new-btn');
    expect(newBtn).not.toBeNull();
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
});
