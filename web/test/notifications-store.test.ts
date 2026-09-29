// The store's one real job: raise a toast for what is genuinely new, never
// for the backlog a tab finds waiting the moment it opens.
//
// The API is mocked at the client module, the same boundary usage-refresh's
// tests mock at, so the interval, the cursor and the toast queue under test
// are all real code.

import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';

vi.mock('@/api/client', () => ({
  api: {
    get: vi.fn() as unknown as <T>(url: string) => Promise<T>,
    post: vi.fn() as unknown as <T>(url: string, body?: unknown) => Promise<T>,
  },
}));
vi.mock('@/router', () => ({ router: { push: vi.fn() } }));

import { api } from '@/api/client';
import type { Notification } from '@/api/notifications';
import { router } from '@/router';
import {
  activeToasts, notificationList, notificationsUnread, openNotification,
  startNotifications, stopNotifications,
} from '@/stores/notifications';

const get = vi.mocked(api.get);
const post = vi.mocked(api.post);

const POLL_MS = 30_000;

function row(id: string, createdAt: number, kind = 'announcement', link = '/'): Notification {
  return { id, kind, params: {}, link, created_at: createdAt };
}

beforeEach(() => {
  vi.useFakeTimers();
});

afterEach(() => {
  stopNotifications();
  vi.clearAllMocks();
  vi.useRealTimers();
});

describe('the notifications store', () => {
  it('loads the backlog into the list without raising a single toast', async () => {
    get.mockImplementation(async (url: string) =>
      (url.includes('/poll') ? { notifications: [], unread: 0, now: 5000 } :
        { notifications: [row('a', 1000), row('b', 2000)], unread: 2, seen_at: 0, now: 5000 }));

    startNotifications();
    await vi.advanceTimersByTimeAsync(0);

    expect(notificationList.value).toHaveLength(2);
    expect(notificationsUnread.value).toBe(2);
    expect(activeToasts.value).toHaveLength(0);
  });

  it('raises a toast only for what a later poll finds that is newer than the backlog', async () => {
    get.mockImplementation(async (url: string) =>
      (url.includes('/poll') ? { notifications: [row('c', 3000)], unread: 1, now: 3000 } :
        { notifications: [row('a', 1000)], unread: 1, seen_at: 0, now: 1000 }));

    startNotifications();
    await vi.advanceTimersByTimeAsync(0); // the initial load — no toast
    expect(activeToasts.value).toHaveLength(0);

    await vi.advanceTimersByTimeAsync(POLL_MS); // the interval's first tick
    expect(activeToasts.value.map((toast) => toast.id)).toEqual(['c']);
    // The new row joins the top of the list too, not only the toast queue.
    expect(notificationList.value[0]?.id).toBe('c');
    expect(notificationList.value).toHaveLength(2);
  });

  it('an unchanged poll raises nothing new', async () => {
    get.mockImplementation(async (url: string) =>
      (url.includes('/poll') ? { notifications: [], unread: 0, now: 1000 } :
        { notifications: [row('a', 1000)], unread: 1, seen_at: 0, now: 1000 }));

    startNotifications();
    await vi.advanceTimersByTimeAsync(0);
    await vi.advanceTimersByTimeAsync(POLL_MS);
    await vi.advanceTimersByTimeAsync(POLL_MS);

    expect(activeToasts.value).toHaveLength(0);
    expect(notificationList.value).toHaveLength(1);
  });

  it('does not poll from a tab nobody can see, and catches up once it is shown', async () => {
    get.mockImplementation(async (url: string) =>
      (url.includes('/poll') ? { notifications: [row('c', 3000)], unread: 1, now: 3000 } :
        { notifications: [], unread: 0, seen_at: 0, now: 1000 }));
    const state = vi.spyOn(document, 'visibilityState', 'get');

    startNotifications();
    await vi.advanceTimersByTimeAsync(0);
    state.mockReturnValue('hidden');
    document.dispatchEvent(new Event('visibilitychange'));
    get.mockClear();

    await vi.advanceTimersByTimeAsync(POLL_MS * 3);
    expect(get).not.toHaveBeenCalled();

    state.mockReturnValue('visible');
    document.dispatchEvent(new Event('visibilitychange'));
    await vi.advanceTimersByTimeAsync(0);
    expect(get.mock.calls.some(([url]) => String(url).includes('/poll'))).toBe(true);
    expect(activeToasts.value.map((toast) => toast.id)).toEqual(['c']);
    state.mockRestore();
  });

  it('forgets everything on sign-out, for the next account', async () => {
    get.mockResolvedValue({ notifications: [row('a', 1000)], unread: 1, seen_at: 0, now: 1000 });
    startNotifications();
    await vi.advanceTimersByTimeAsync(0);
    expect(notificationList.value).toHaveLength(1);

    stopNotifications();
    expect(notificationList.value).toHaveLength(0);
    expect(notificationsUnread.value).toBe(0);
    expect(activeToasts.value).toHaveLength(0);
  });

  it('opening a notification navigates, drops its toast and moves the watermark', async () => {
    get.mockImplementation(async (url: string) =>
      (url.includes('/poll') ? { notifications: [row('c', 3000, 'quota_reset', '/usage')], unread: 1, now: 3000 } :
        { notifications: [], unread: 0, seen_at: 0, now: 0 }));
    post.mockResolvedValue(undefined);

    startNotifications();
    await vi.advanceTimersByTimeAsync(0);
    await vi.advanceTimersByTimeAsync(POLL_MS);
    const toast = activeToasts.value[0];
    expect(toast).toBeDefined();

    await openNotification(toast!.notification);

    expect(router.push).toHaveBeenCalledWith('/usage');
    expect(activeToasts.value).toHaveLength(0);
    expect(post).toHaveBeenCalledWith('/api/notifications/read', { up_to: 3000 });
    expect(notificationsUnread.value).toBe(0);
  });

  it('shows only the newest three toasts from an oversized batch, though every row still reaches the list', async () => {
    get.mockImplementation(async (url: string) =>
      (url.includes('/poll')
        ? { notifications: [row('c', 3000), row('d', 4000), row('e', 5000), row('f', 6000)], unread: 4, now: 6000 }
        : { notifications: [], unread: 0, seen_at: 0, now: 1000 }));

    startNotifications();
    await vi.advanceTimersByTimeAsync(0);
    await vi.advanceTimersByTimeAsync(POLL_MS);

    // The three most recent of the four, not the three that happened to poll
    // in first.
    expect(activeToasts.value.map((toast) => toast.id)).toEqual(['d', 'e', 'f']);
    // The bell's own list is not a toast stack — it keeps all four.
    expect(notificationList.value).toHaveLength(4);
  });

  it('does not toast or list the same row twice when the grace window re-returns it', async () => {
    get.mockImplementation(async (url: string) =>
      (url.includes('/poll') ? { notifications: [row('c', 3000)], unread: 1, now: 3000 } :
        { notifications: [], unread: 0, seen_at: 0, now: 1000 }));

    startNotifications();
    await vi.advanceTimersByTimeAsync(0);
    await vi.advanceTimersByTimeAsync(POLL_MS); // row c arrives
    expect(activeToasts.value).toHaveLength(1);
    expect(notificationList.value).toHaveLength(1);

    // The next tick's grace window hands back the exact same row — the
    // server does this on purpose, so the id de-dupe is what keeps it from
    // becoming a second toast or a second list entry.
    await vi.advanceTimersByTimeAsync(POLL_MS);
    expect(activeToasts.value).toHaveLength(1);
    expect(notificationList.value).toHaveLength(1);
  });

  it('drops a poll that fires while the previous one is still in flight', async () => {
    let resolveFirst!: (value: unknown) => void;
    const first = new Promise((resolve) => { resolveFirst = resolve; });
    let pollCalls = 0;
    get.mockImplementation(async (url: string) => {
      if (!url.includes('/poll')) return { notifications: [], unread: 0, seen_at: 0, now: 1000 };
      pollCalls += 1;
      return pollCalls === 1 ? first : { notifications: [], unread: 0, now: 9000 };
    });

    startNotifications();
    await vi.advanceTimersByTimeAsync(0); // the initial load
    await vi.advanceTimersByTimeAsync(POLL_MS); // first poll tick — hangs on `first`
    await vi.advanceTimersByTimeAsync(POLL_MS); // a second tick while the first is still pending

    expect(pollCalls).toBe(1); // the in-flight guard skipped the second tick entirely

    resolveFirst({ notifications: [row('b', 2000)], unread: 1, now: 2000 });
    await vi.advanceTimersByTimeAsync(0);
    expect(activeToasts.value.map((toast) => toast.id)).toEqual(['b']);
  });

  it('drops a response that lands after sign-out instead of writing into the next session', async () => {
    let resolvePoll!: (value: unknown) => void;
    const pending = new Promise((resolve) => { resolvePoll = resolve; });
    get.mockImplementation(async (url: string) =>
      (url.includes('/poll') ? pending : { notifications: [], unread: 0, seen_at: 0, now: 1000 }));

    startNotifications();
    await vi.advanceTimersByTimeAsync(0);
    await vi.advanceTimersByTimeAsync(POLL_MS); // fires the poll that will hang on `pending`

    stopNotifications();
    resolvePoll({ notifications: [row('z', 9999)], unread: 1, now: 9999 });
    await vi.advanceTimersByTimeAsync(0);

    expect(activeToasts.value).toHaveLength(0);
    expect(notificationList.value).toHaveLength(0);
    expect(notificationsUnread.value).toBe(0);
  });
});
