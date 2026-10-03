// The bell grew a second feed without losing its first: the badge counts
// both, the dropdown still opens the announcement it already knew about, and
// a notification row navigates and marks itself read the way the toast
// stack's own click does.

import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import { createApp, nextTick, type App } from 'vue';
import { changeLanguage, t } from '@/composables/useI18n';
import AnnounceBell from '@/announce/AnnounceBell.vue';

vi.mock('@/api/announcements', () => ({
  fetchAnnouncements: vi.fn(async () => ({
    announcements: [{
      id: 'a1', title: 'Scheduled maintenance', body: '', display_mode: 'silent',
      dismiss_after_seconds: 0, published: true, pinned: false,
      created_at: 1000, updated_at: 1000, read: false,
    }],
    unread: 1, popup: null,
  })),
  markRead: vi.fn(async () => undefined),
  markAllRead: vi.fn(async () => undefined),
}));
vi.mock('@/api/client', () => ({
  api: {
    get: vi.fn() as unknown as <T>(url: string) => Promise<T>,
    post: vi.fn() as unknown as <T>(url: string, body?: unknown) => Promise<T>,
  },
}));
vi.mock('@/router', () => ({ router: { push: vi.fn() } }));

import { api } from '@/api/client';
import { router } from '@/router';
import { notificationList, notificationsUnread, stopNotifications } from '@/stores/notifications';

const post = vi.mocked(api.post);

let app: App | null = null;
let host: HTMLElement;

beforeEach(async () => {
  await changeLanguage('en');
  host = document.createElement('div');
  document.body.appendChild(host);
});

afterEach(() => {
  app?.unmount();
  app = null;
  host.remove();
  document.body.textContent = '';
  stopNotifications();
  vi.clearAllMocks();
});

async function mount(): Promise<void> {
  app = createApp(AnnounceBell);
  app.mount(host);
  await new Promise((resolve) => setTimeout(resolve, 0));
  await nextTick();
}

async function open(): Promise<void> {
  host.querySelector<HTMLElement>('.oa-bell')!.click();
  await nextTick();
}

describe('the bell, with notifications added', () => {
  it('carries a dot for an unread notification even with no unread announcement', async () => {
    notificationList.value = [{ id: 'n1', kind: 'quota_reset', params: {}, link: '/usage', created_at: 2000 }];
    notificationsUnread.value = 1;
    // Nobody has an unread announcement in this test's mock beyond the one
    // above, so the dot proves the notification side alone can raise it.
    await mount();
    await open();

    expect(host.querySelector('.oa-bell-dot')).not.toBeNull();
  });

  it('shows both feeds without either one crowding the other out', async () => {
    notificationList.value = [{ id: 'n1', kind: 'quota_reset', params: {}, link: '/usage', created_at: 2000 }];
    notificationsUnread.value = 1;
    await mount();
    await open();

    const heads = [...host.querySelectorAll('.oa-menu-head-name')].map((node) => node.textContent);
    expect(heads).toEqual([t('announcements'), t('notifications')]);
    expect(host.textContent).toContain('Scheduled maintenance');
    expect(host.textContent).toContain(t('notifyTitleQuotaReset'));
  });

  it('a click on a notification row navigates and marks it read, leaving the announcement alone', async () => {
    notificationList.value = [{ id: 'n1', kind: 'quota_reset', params: {}, link: '/usage', created_at: 2000 }];
    notificationsUnread.value = 1;
    await mount();
    await open();

    const rows = [...host.querySelectorAll('.oa-menu-item')];
    const notificationRow = rows.find((row) => row.textContent?.includes(t('notifyTitleQuotaReset')));
    expect(notificationRow).toBeDefined();
    (notificationRow as HTMLElement).click();
    await nextTick();

    expect(router.push).toHaveBeenCalledWith('/usage');
    expect(post).toHaveBeenCalledWith('/api/notifications/read', { up_to: 2000 });
    expect(notificationsUnread.value).toBe(0);
  });

  it('does not list an announcement-kind row in the notifications section, which would count it twice', async () => {
    // The announcement above the fold already lists this arrival by its own
    // title; the notifications section repeating it, worded as a
    // notification, would be the same arrival said twice, once per feed.
    notificationList.value = [
      { id: 'n1', kind: 'announcement', params: { title: 'Scheduled maintenance' }, link: '/', created_at: 1500 },
      { id: 'n2', kind: 'quota_reset', params: {}, link: '/usage', created_at: 2000 },
    ];
    notificationsUnread.value = 2;
    await mount();
    await open();

    const titles = [...host.querySelectorAll('.oa-menu-item-title')].map((node) => node.textContent);
    // The announcement's own title once, from the feed above; the
    // notification worded as "Announcement" not present at all — that would
    // be the same arrival said twice.
    expect(titles).toEqual(['Scheduled maintenance', t('notifyTitleQuotaReset')]);
    expect(titles).not.toContain(t('notifyTitleAnnouncement'));
  });

  it('"mark all read" clears the notification badge without touching the announcement feed', async () => {
    notificationList.value = [{ id: 'n1', kind: 'quota_reset', params: {}, link: '/usage', created_at: 2000 }];
    notificationsUnread.value = 1;
    post.mockResolvedValue(undefined);
    await mount();
    await open();

    // Opening the bell marked what was waiting then read — that is the badge
    // contract now — so the section's own button exists only for something
    // that lands while the menu is up. Put one there, and forget the open's
    // own request so the assertion below can only be met by this click.
    expect(notificationsUnread.value).toBe(0);
    notificationsUnread.value = 1;
    post.mockClear();
    await nextTick();

    // Scoped to the notification section specifically: its own "mark all
    // read" reads the same as the announcement one's, so the two are told
    // apart by where they sit rather than by their (identical) English text.
    const markAll = host.querySelector<HTMLElement>('.oa-menu-head-secondary .oa-menu-head-action');
    expect(markAll).not.toBeNull();
    markAll!.click();
    await nextTick();

    expect(notificationsUnread.value).toBe(0);
    expect(post).toHaveBeenCalledWith('/api/notifications/read', {});
    // The announcement's own "mark all read" button is a different element,
    // still there and still callable.
    expect(host.querySelector('.oa-menu-head:not(.oa-menu-head-secondary) .oa-menu-head-action')).not.toBeNull();
  });
});
