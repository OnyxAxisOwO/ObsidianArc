// What is new since the reader last looked: the bell's own count and list,
// and the toast queue that raises one card per genuinely new arrival.
//
// Plain refs and functions, like the other stores here: this is a handful of
// values and half a dozen actions, not a state-management library's job.
//
// Polling rather than a push channel — the same choice the announcement bell
// made — because the payload is small, thirty seconds is not urgent, and a
// socket would be a fifth direct dependency for one feature. See
// docs/ARCHITECTURE.md on why that line gets drawn carefully here.

import { ref, watch, type Ref } from 'vue';
import { useDocumentVisibility, useIntervalFn, useWindowFocus } from '@vueuse/core';
import { fetchNotifications, markNotificationsRead, pollNotifications, type Notification } from '@/api/notifications';
import { router } from '@/router';

const POLL_MS = 30_000;

export interface Toast {
  /** The notification's own id doubles as the toast's — one arrival, one card. */
  id: string;
  notification: Notification;
}

const notifications = ref<Notification[]>([]);
const unread = ref(0);
const toasts = ref<Toast[]>([]);

export const notificationList: Ref<Notification[]> = notifications;
export const notificationsUnread: Ref<number> = unread;
export const activeToasts: Ref<Toast[]> = toasts;

// The most a reader can be shown at once without the stack becoming its own
// notification backlog; a burst still reaches the bell list in full, just not
// the corner of the screen.
const TOAST_CAP = 3;

// The highest created_at this session has already asked about. Zero at boot,
// which is why the very first fetch is a plain list rather than a poll: a
// poll "since 0" would raise a toast for the whole backlog the moment
// somebody opens the app.
let cursor = 0;
let loaded = false;
let active = false;

// Bumped by start/stop and captured before every await, so a response that
// lands after a sign-out (or after the next account's own start) is dropped
// instead of writing into a session it no longer belongs to.
let generation = 0;
// Only one request in flight at a time: an overlapping load/poll pair would
// otherwise both merge the same rows in, independently of the id de-dupe
// below.
let inFlight = false;

// Every id this session has already put in front of the reader, in the list
// or as a toast. The poll endpoint re-returns anything inside its grace
// window on purpose (a row committed late must not be missed), so the client
// is where the duplicate stops.
const seenIds = new Set<string>();

async function load(): Promise<void> {
  const gen = generation;
  inFlight = true;
  try {
    const feed = await fetchNotifications();
    if (gen !== generation) return; // stale: signed out (or in) since the request went out
    notifications.value = feed.notifications;
    unread.value = feed.unread;
    let newest = 0;
    for (const record of feed.notifications) {
      seenIds.add(record.id);
      newest = Math.max(newest, record.created_at);
    }
    // Not just the newest row's own timestamp: an account whose visibility
    // widens later (promoted to admin) must not have the server's whole
    // backlog for its new scope raised as toasts on the next poll.
    cursor = Math.max(newest, feed.now);
    loaded = true;
  } catch {
    // A feed nobody could reach is not worth an error state; the next tick
    // tries again, and whatever this session last knew stays on screen.
  } finally {
    if (gen === generation) inFlight = false;
  }
}

async function poll(): Promise<void> {
  if (!active || inFlight) return;
  if (!loaded) {
    await load();
    return;
  }
  const gen = generation;
  inFlight = true;
  try {
    const feed = await pollNotifications(cursor);
    if (gen !== generation) return;
    unread.value = feed.unread;
    if (!feed.notifications.length) return;
    const fresh = feed.notifications.filter((record) => !seenIds.has(record.id));
    // Every row received advances the cursor, seen or not — a row this
    // session already has just should not be shown again, not asked about
    // again.
    for (const record of feed.notifications) {
      seenIds.add(record.id);
      cursor = Math.max(cursor, record.created_at);
    }
    if (!fresh.length) return;
    // Ascending from the server, oldest first, so the toasts raised below
    // appear in the order the events actually happened, and so the cap below
    // keeps the newest of the batch rather than the earliest.
    const batch = fresh.map((record) => ({ id: record.id, notification: record }));
    // A reassignment, not a push: Vue 3.5's watch(activeToasts, ...) only
    // fires for a new array reference, and a push mutates the one it already
    // saw.
    toasts.value = [...toasts.value, ...batch].slice(-TOAST_CAP);
    const newestFirst = [...fresh].reverse();
    notifications.value = [...newestFirst, ...notifications.value].slice(0, 50);
  } catch {
    // Transient network hiccup; the next tick tries again.
  } finally {
    if (gen === generation) inFlight = false;
  }
}

// A tab nobody can see does not poll. Every open tab used to ask every thirty
// seconds, hidden or not, and each ask is a session lookup and three queries
// on the server — a reader with a dozen tabs open paid for all of them. The
// visible tab still polls, and a hidden one catches up the moment it is shown.
const visibility = useDocumentVisibility();
const interval = useIntervalFn(() => {
  if (visibility.value !== 'hidden') void poll();
}, POLL_MS, { immediate: false });
watch(visibility, (now, was) => {
  if (now === 'visible' && was === 'hidden') void poll();
});

// "Immediately when the window regains focus": a tab put down for an hour
// and picked back up should not wait up to thirty seconds to say so.
const focused = useWindowFocus();
watch(focused, (isFocused, wasFocused) => {
  if (isFocused && !wasFocused) void poll();
});

/** Called once, from the signed-in shell, whenever an account is present. */
export function startNotifications(): void {
  if (active) return;
  active = true;
  generation += 1;
  seenIds.clear();
  toasts.value = [];
  cursor = 0;
  loaded = false;
  void load();
  interval.resume();
}

/** Called on sign-out: the next account's inbox is not this one's. */
export function stopNotifications(): void {
  active = false;
  generation += 1; // orphans any request already in flight
  inFlight = false;
  interval.pause();
  notifications.value = [];
  unread.value = 0;
  toasts.value = [];
  seenIds.clear();
  cursor = 0;
  loaded = false;
}

export function dismissToast(id: string): void {
  toasts.value = toasts.value.filter((toast) => toast.id !== id);
}

/**
 * The bell's "mark all read": clears the badge locally and moves the
 * server's watermark to the newest row this session has actually shown,
 * rather than to the server's own clock — a poll response still in flight
 * when this fires must not be silently marked read too.
 */
export async function markAllNotificationsRead(): Promise<void> {
  unread.value = 0;
  try {
    await markNotificationsRead(cursor || undefined);
  } catch {
    // The next poll's own unread count is the source of truth either way.
  }
}

/**
 * A click on one row, from the toast stack or from the bell's list: go where
 * it points and move the watermark up to it. The watermark has no per-row
 * memory, so this also marks anything older than it read — the same way
 * opening the newest of several unread emails in a single-threaded inbox
 * would.
 */
export async function openNotification(n: Notification): Promise<void> {
  dismissToast(n.id);
  if (n.link) void router.push(n.link);
  unread.value = Math.max(0, unread.value - 1);
  try {
    await markNotificationsRead(n.created_at);
  } catch {
    // Best effort; the next poll's unread count corrects it either way.
  }
}
