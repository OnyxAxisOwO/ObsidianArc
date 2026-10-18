// The visible half of the notification system: a card renders per toast, a
// click on one navigates and marks it read, and a card left alone for six
// seconds removes itself — paused for as long as the pointer sits on it.

import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import { createApp, nextTick, type App } from 'vue';
import { createMemoryHistory, createRouter } from 'vue-router';
import { changeLanguage, t } from '@/composables/useI18n';
import OaToastStack from '@/components/OaToastStack.vue';

vi.mock('@/api/client', () => ({
  api: {
    get: vi.fn(async () => ({ notifications: [], unread: 0, seen_at: 0, now: 0 })) as unknown as <T>(url: string) => Promise<T>,
    post: vi.fn(async () => undefined) as unknown as <T>(url: string, body?: unknown) => Promise<T>,
  },
}));

import { api } from '@/api/client';
import { activeToasts, stopNotifications } from '@/stores/notifications';

// The card navigates through the router it is mounted with. A memory router
// records the push without moving the page.
const router = createRouter({ history: createMemoryHistory(), routes: [{ path: '/:p(.*)*', component: { render: () => null } }] });
const pushRoute = vi.spyOn(router, 'push').mockResolvedValue(undefined);

const POLL_MS = 30_000;

let app: App | null = null;
let host: HTMLElement;

beforeEach(async () => {
  await changeLanguage('en');
  vi.useFakeTimers();
  host = document.createElement('div');
  document.body.appendChild(host);
});

afterEach(() => {
  app?.unmount();
  app = null;
  host.remove();
  stopNotifications();
  vi.clearAllMocks();
  vi.useRealTimers();
});

async function mount(): Promise<void> {
  app = createApp(OaToastStack);
  app.use(router);
  app.mount(host);
  await vi.advanceTimersByTimeAsync(0); // the store's own initial load
  await nextTick();
}

function push(id: string, kind = 'announcement', link = '/'): void {
  activeToasts.value = [...activeToasts.value, {
    id, notification: { id, kind, params: { title: 'Maintenance' }, link, created_at: Date.now() },
  }];
}

describe('OaToastStack', () => {
  it('renders one card per active toast, worded from its kind', async () => {
    await mount();
    push('a');
    await nextTick();

    const cards = host.querySelectorAll('.oa-toast');
    expect(cards).toHaveLength(1);
    expect(cards[0]!.querySelector('.oa-toast-title')?.textContent).toBe(t('notifyTitleAnnouncement'));
    expect(cards[0]!.querySelector('.oa-toast-text')?.textContent).toBe('Maintenance');
  });

  it('a click on the card navigates to its link and removes it', async () => {
    await mount();
    push('a', 'quota_reset', '/usage');
    await nextTick();

    host.querySelector<HTMLElement>('.oa-toast')!.click();
    await nextTick();

    expect(pushRoute).toHaveBeenCalledWith('/usage');
    expect(activeToasts.value).toHaveLength(0);
  });

  it('the close button dismisses without navigating', async () => {
    await mount();
    push('a', 'quota_reset', '/usage');
    await nextTick();

    host.querySelector<HTMLElement>('.oa-toast-close')!.click();
    await nextTick();

    expect(pushRoute).not.toHaveBeenCalled();
    expect(activeToasts.value).toHaveLength(0);
  });

  it('names the close button in a tooltip as well as for a screen reader', async () => {
    await mount();
    push('a');
    await nextTick();

    const close = host.querySelector<HTMLButtonElement>('.oa-toast-close')!;
    expect(close.title).toBe(t('close'));
    expect(close.getAttribute('aria-label')).toBe(t('close'));
  });

  it('auto-dismisses six seconds after it appears', async () => {
    await mount();
    push('a');
    await nextTick();

    await vi.advanceTimersByTimeAsync(5999);
    expect(activeToasts.value).toHaveLength(1);
    await vi.advanceTimersByTimeAsync(1);
    expect(activeToasts.value).toHaveLength(0);
  });

  it('hovering pauses the dismissal, and leaving resumes it', async () => {
    await mount();
    push('a');
    await nextTick();
    const card = host.querySelector<HTMLElement>('.oa-toast')!;

    await vi.advanceTimersByTimeAsync(4000);
    card.dispatchEvent(new Event('mouseenter'));
    // Well past six seconds since the card appeared — it must still be here,
    // because four of those seconds were spent hovered.
    await vi.advanceTimersByTimeAsync(4000);
    expect(activeToasts.value).toHaveLength(1);

    card.dispatchEvent(new Event('mouseleave'));
    // Only ~2s of the original 6s had elapsed when the pointer arrived.
    await vi.advanceTimersByTimeAsync(1999);
    expect(activeToasts.value).toHaveLength(1);
    await vi.advanceTimersByTimeAsync(1);
    expect(activeToasts.value).toHaveLength(0);
  });

  it('a toast the store adds through a real poll still auto-dismisses', async () => {
    // Unlike push() above, this goes through the store's own poll() — the
    // path that used to mutate the queue in place instead of reassigning it,
    // which left this component's watch(activeToasts) silent and the card on
    // screen forever.
    vi.mocked(api.get).mockImplementation(async (url: string) => (url.includes('/poll')
      ? { notifications: [{ id: 'p1', kind: 'quota_reset', params: {}, link: '/usage', created_at: 5000 }], unread: 1, now: 5000 }
      : { notifications: [], unread: 0, seen_at: 0, now: 0 }));

    await mount(); // the store's initial (empty) load
    await vi.advanceTimersByTimeAsync(POLL_MS); // the interval's first real poll
    await nextTick();

    expect(host.querySelectorAll('.oa-toast')).toHaveLength(1);

    await vi.advanceTimersByTimeAsync(5999);
    expect(activeToasts.value).toHaveLength(1);
    await vi.advanceTimersByTimeAsync(1);
    expect(activeToasts.value).toHaveLength(0);
  });
});
