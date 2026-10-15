// The bell fetches the feed from several places at once, and it is remounted
// whenever the reader moves between the chat and the backoffice. Two things
// must hold: a slow response from an earlier refresh cannot undo a newer one,
// and an "every visit" popup already shown this page load does not come back
// when the bell is remounted.

import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import { createApp, nextTick, type App } from 'vue';
import AnnounceBell from '@/announce/AnnounceBell.vue';
import { fetchAnnouncement, fetchAnnouncements, markAllRead, type Announcement, type AnnouncementFeed } from '@/api/announcements';
import { notificationList, notificationsUnread, stopNotifications } from '@/stores/notifications';

vi.mock('@/api/announcements', () => ({
  fetchAnnouncements: vi.fn(),
  fetchAnnouncement: vi.fn(),
  markRead: vi.fn(async () => undefined),
  markAllRead: vi.fn(async () => undefined),
}));
vi.mock('@/api/client', () => ({
  api: {
    get: vi.fn(),
    post: vi.fn(async () => undefined),
  },
}));
vi.mock('@/router', () => ({ router: { push: vi.fn() } }));

const fetchMock = vi.mocked(fetchAnnouncements);
const markAllMock = vi.mocked(markAllRead);
const fetchOneMock = vi.mocked(fetchAnnouncement);

function announcement(overrides: Partial<Announcement> = {}): Announcement {
  return {
    id: 'a1', title: 'Scheduled maintenance', body: 'Tonight.', display_mode: 'silent',
    dismiss_after_seconds: 0, published: true, pinned: false,
    created_at: 1000, updated_at: 1000, read: false,
    ...overrides,
  };
}

function feed(overrides: Partial<AnnouncementFeed> = {}): AnnouncementFeed {
  return { announcements: [announcement()], unread: 1, popup: null, ...overrides };
}

function deferred<T>(): { promise: Promise<T>; resolve: (value: T) => void } {
  let resolve!: (value: T) => void;
  const promise = new Promise<T>((done) => { resolve = done; });
  return { promise, resolve };
}

function settle(): Promise<void> {
  return new Promise((resolve) => setTimeout(resolve, 0)).then(() => nextTick());
}

let app: App | null = null;
let host: HTMLElement;

beforeEach(() => {
  host = document.createElement('div');
  document.body.appendChild(host);
  notificationList.value = [];
  notificationsUnread.value = 0;
  fetchMock.mockReset();
});

afterEach(() => {
  app?.unmount();
  app = null;
  host.remove();
  document.body.textContent = '';
  stopNotifications();
  vi.clearAllMocks();
});

function mountBell(): void {
  app = createApp(AnnounceBell);
  app.mount(host);
}

function unmountBell(): void {
  app?.unmount();
  app = null;
  host.textContent = '';
}

async function openBell(): Promise<void> {
  host.querySelector<HTMLElement>('.oa-bell')!.click();
  await nextTick();
}

describe('announcement bell refreshes', () => {
  it('keeps the newest feed when an older refresh answers last', async () => {
    const older = deferred<AnnouncementFeed>();
    const newer = deferred<AnnouncementFeed>();
    // Mount starts the first refresh, opening the bell starts the second.
    fetchMock.mockReturnValueOnce(older.promise).mockReturnValueOnce(newer.promise);
    mountBell();
    await openBell();

    newer.resolve(feed({ announcements: [announcement({ read: true })], unread: 0 }));
    await settle();
    older.resolve(feed({ unread: 1 }));
    await settle();

    expect(host.querySelector('.oa-bell-dot')).toBeNull();
    expect(host.querySelector('.oa-menu-unread')).toBeNull();
  });

  it('still shows the popup its refresh asked for when a later refresh answers first', async () => {
    const popup = announcement({ id: 'overtaken-popup', display_mode: 'always' });
    const mount = deferred<AnnouncementFeed>();
    const opened = deferred<AnnouncementFeed>();
    // Mount asks for the popup; opening the bell asks for none, and answers first.
    fetchMock.mockReturnValueOnce(mount.promise).mockReturnValueOnce(opened.promise);
    mountBell();
    await openBell();

    opened.resolve(feed());
    await settle();
    expect(document.querySelector('.oa-announce')).toBeNull();

    mount.resolve(feed({ popup }));
    await settle();
    expect(document.querySelector('.oa-announce')).not.toBeNull();
  });

  it('does not show a popup again after the bell is remounted in the same page load', async () => {
    const popup = announcement({ id: 'every-visit', display_mode: 'always' });
    fetchMock.mockResolvedValue(feed({ popup }));
    mountBell();
    await settle();
    expect(document.querySelector('.oa-announce')).not.toBeNull();

    // Dismissing the sheet is what the reader does; the bell then leaves the
    // page (chat to backoffice) and comes back.
    document.querySelector<HTMLElement>('.oa-announce-foot button')?.click();
    await nextTick();
    unmountBell();
    mountBell();
    await settle();

    expect(document.querySelector('.oa-announce')).toBeNull();
  });

  it('a refresh that answers after the bell left the page does not use up its popup', async () => {
    const popup = announcement({ id: 'late-popup', display_mode: 'always' });
    const late = deferred<AnnouncementFeed>();
    // The first bell's refresh is still in flight when the bell leaves the page.
    fetchMock.mockReturnValueOnce(late.promise).mockResolvedValue(feed({ popup }));
    mountBell();
    unmountBell();
    late.resolve(feed({ popup }));
    await settle();

    mountBell();
    await settle();

    expect(document.querySelector('.oa-announce')).not.toBeNull();
  });

  it('shows the row opened last when an earlier row body answers after it', async () => {
    const first = deferred<Announcement>();
    const second = deferred<Announcement>();
    fetchMock.mockResolvedValue(feed({
      announcements: [announcement({ id: 'a1', title: 'First row' }), announcement({ id: 'a2', title: 'Second row' })],
      unread: 0,
    }));
    fetchOneMock.mockImplementation((id) => (id === 'a1' ? first.promise : second.promise));
    const rowTitled = (title: string) => [...host.querySelectorAll<HTMLElement>('.oa-menu-item')]
      .find((item) => item.textContent?.includes(title));
    mountBell();
    await settle();

    await openBell();
    rowTitled('First row')!.click();
    await nextTick();
    await openBell();
    rowTitled('Second row')!.click();
    await nextTick();

    second.resolve(announcement({ id: 'a2', title: 'Second row', body: 'Second body.' }));
    await settle();
    first.resolve(announcement({ id: 'a1', title: 'First row', body: 'First body.' }));
    await settle();

    const body = document.querySelector('.oa-announce-body')?.textContent ?? '';
    expect(body).toContain('Second body.');
    expect(body).not.toContain('First body.');
  });

  it('opening a row from the list fetches its body before the sheet shows', async () => {
    fetchMock.mockResolvedValue(feed({ announcements: [announcement({ id: 'a1', title: 'Maintenance', body: '' })] }));
    fetchOneMock.mockResolvedValue(announcement({ id: 'a1', title: 'Maintenance', body: 'Tonight from 22:00.' }));
    mountBell();
    await settle();
    await openBell();

    const row = [...host.querySelectorAll<HTMLElement>('.oa-menu-item')].find((item) => item.textContent?.includes('Maintenance'));
    row!.click();
    await settle();

    expect(fetchOneMock).toHaveBeenCalledWith('a1');
    expect(document.querySelector('.oa-announce-body')?.textContent).toContain('Tonight from 22:00.');
  });

  it('a failed "mark all read" still reloads the feed instead of leaving it unhandled', async () => {
    fetchMock.mockResolvedValue(feed());
    markAllMock.mockRejectedValueOnce(new Error('offline'));
    mountBell();
    await settle();
    await openBell();
    const before = fetchMock.mock.calls.length;

    host.querySelector<HTMLElement>('.oa-menu-head:not(.oa-menu-head-secondary) .oa-menu-head-action')!.click();
    await settle();

    expect(markAllMock).toHaveBeenCalledTimes(1);
    expect(markAllMock).toHaveBeenCalledWith(['a1']);
    expect(fetchMock.mock.calls.length).toBeGreaterThan(before);
  });
});
