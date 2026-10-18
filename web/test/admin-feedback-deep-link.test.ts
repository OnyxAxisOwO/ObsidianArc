// A notification's feedback_new link (/admin/feedback/<id>) has to land on
// that one thread, not on the unfiltered list — AdminPage keeps AdminFeedback
// mounted across that navigation (same slug, a different trailing segment),
// so this exercises both the first visit and a second link clicked while the
// page is already open, the one onMounted alone cannot catch.

import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import { createApp, h, reactive, shallowRef, type App } from 'vue';
import { adminApi } from '../src/admin/api';
import { ApiError } from '../src/api/client';
import type { Feedback, FeedbackReply, FeedbackThread } from '../src/api/feedback';
import { changeLanguage, t } from '../src/composables/useI18n';
import { providePanelHost } from '../src/composables/usePanelHost';
import AdminFeedback from '../src/views/admin/AdminFeedback.vue';
import { provideAdminView } from '../src/views/admin/adminView';
import { settle } from './helpers';

let app: App | undefined;
let host: HTMLElement;
let panels: HTMLElement;
let actions: HTMLElement;

beforeEach(async () => {
  await changeLanguage('en');
  host = document.createElement('div');
  panels = document.createElement('div');
  actions = document.createElement('div');
  document.body.append(host, panels, actions);
});

afterEach(() => {
  app?.unmount();
  app = undefined;
  vi.restoreAllMocks();
});


function record(over: Partial<Feedback> = {}): Feedback {
  return {
    id: 'f1', user_id: 'u1', kind: 'bug', priority: 'high', status: 'open',
    title: 'Streaming stops', body: 'It stops half way.',
    created_at: Date.now(), updated_at: Date.now(), username: 'reader', nickname: 'Reader',
    replies: 0, author_unread: false, operator_unread: true,
    ...over,
  };
}

function thread(over: Partial<FeedbackThread> = {}): FeedbackThread {
  return { feedback: record(), replies: [], ...over };
}

function emptyList() {
  return vi.spyOn(adminApi, 'feedback').mockResolvedValue({
    feedback: [], total: 0, offset: 0,
    summary: { total: 0, open: 0, bugs: 0, ideas: 0, high_open: 0, awaiting: 0 },
  });
}

/**
 * Mounts the page behind a `params` getter that reads a reactive object,
 * the same way AdminPage's own `params` reads its route-derived `segments`
 * computed — so re-assigning `state.params` here is what a second
 * notification link changes underneath the mounted page.
 */
function mountWithParams(initial: string[]): { params: string[] } {
  const state = reactive({ params: initial });
  app = createApp({
    setup() {
      providePanelHost(shallowRef(panels));
      provideAdminView({
        actionsHost: actions,
        setTitle: () => {},
        reload: () => {},
        get params() {
          return state.params;
        },
      });
      return () => h(AdminFeedback);
    },
  });
  app.mount(host);
  return state;
}

describe('opening a feedback thread from its id in the URL', () => {
  it('opens the thread on mount, with no list row to show first', async () => {
    emptyList();
    const fetchThread = vi.spyOn(adminApi, 'feedbackThread').mockResolvedValue(thread());
    mountWithParams(['f1']);
    await settle();

    expect(fetchThread).toHaveBeenCalledWith('f1');
    expect(panels.textContent).toContain('Streaming stops');
  });

  it('shows the deep-linked report\'s own row as selected once the list arrives', async () => {
    vi.spyOn(adminApi, 'feedback').mockResolvedValue({
      feedback: [record({ operator_unread: true })], total: 1, offset: 0,
      summary: { total: 1, open: 1, bugs: 1, ideas: 0, high_open: 0, awaiting: 1 },
    });
    vi.spyOn(adminApi, 'feedbackThread').mockResolvedValue(thread());
    mountWithParams(['f1']);
    await settle();

    const card = host.querySelector('.oa-feedback-card')!;
    expect(card.classList.contains('selected')).toBe(true);
  });

  it('opens a second thread when the id in the params changes without a remount', async () => {
    emptyList();
    const fetchThread = vi.spyOn(adminApi, 'feedbackThread')
      .mockResolvedValueOnce(thread({ feedback: record({ id: 'f1', title: 'First report' }) }))
      .mockResolvedValueOnce(thread({ feedback: record({ id: 'f2', title: 'Second report' }) }));
    const state = mountWithParams(['f1']);
    await settle();
    expect(panels.textContent).toContain('First report');

    // AdminPage does not remount the page for this — same slug, a new
    // trailing segment — so only the watch on params catches it.
    state.params = ['f2'];
    await settle();

    expect(fetchThread).toHaveBeenCalledTimes(2);
    expect(fetchThread).toHaveBeenLastCalledWith('f2');
    expect(panels.textContent).toContain('Second report');
    expect(panels.textContent).not.toContain('First report');
  });

  it('leaves the list on its own when no id is in the params', async () => {
    const list = emptyList();
    const fetchThread = vi.spyOn(adminApi, 'feedbackThread');
    mountWithParams([]);
    await settle();

    expect(list).toHaveBeenCalled();
    expect(fetchThread).not.toHaveBeenCalled();
    expect(panels.textContent ?? '').toBe('');
  });
});

function deferred<T>(): { promise: Promise<T>; resolve: (value: T) => void } {
  let resolve!: (value: T) => void;
  const promise = new Promise<T>((done) => { resolve = done; });
  return { promise, resolve };
}

describe('a report whose thread fails to load', () => {
  it('says so on the page when a notification link names a report it cannot open', async () => {
    emptyList();
    vi.spyOn(adminApi, 'feedbackThread').mockRejectedValue(new ApiError(404, 'not_found', 'No such report'));
    mountWithParams(['gone']);
    await settle();

    expect(host.querySelector('#feedbackList')?.textContent).toContain('No such report');
    expect(panels.textContent).not.toContain(t('loading'));
  });

  it('shows the error in the panel rather than a spinner when a clicked row fails', async () => {
    vi.spyOn(adminApi, 'feedback').mockResolvedValue({
      feedback: [record({ title: 'Broken open' })], total: 1, offset: 0,
      summary: { total: 1, open: 1, bugs: 1, ideas: 0, high_open: 0, awaiting: 1 },
    });
    vi.spyOn(adminApi, 'feedbackThread').mockRejectedValue(new ApiError(500, 'boom', 'Thread unavailable'));
    mountWithParams([]);
    await settle();

    host.querySelector<HTMLButtonElement>('.oa-feedback-card')!.click();
    await settle();

    expect(panels.textContent).toContain('Thread unavailable');
    expect(panels.textContent).not.toContain(t('loading'));
  });

  it('does not keep the previous report in the panel when a link names one that fails', async () => {
    vi.spyOn(adminApi, 'feedback').mockResolvedValue({
      feedback: [record({ id: 'a', title: 'Alpha report' })], total: 1, offset: 0,
      summary: { total: 1, open: 1, bugs: 1, ideas: 0, high_open: 0, awaiting: 1 },
    });
    vi.spyOn(adminApi, 'feedbackThread')
      .mockResolvedValueOnce(thread({ feedback: record({ id: 'a', title: 'Alpha report' }) }))
      .mockRejectedValueOnce(new ApiError(404, 'not_found', 'No such report'));
    const state = mountWithParams([]);
    await settle();

    host.querySelector<HTMLButtonElement>('.oa-feedback-card')!.click();
    await settle();
    expect(panels.textContent).toContain('Alpha report');

    state.params = ['gone'];
    await settle();

    expect(host.querySelector('#feedbackList')?.textContent).toContain('No such report');
    expect(panels.textContent).not.toContain('Alpha report');
  });

  it('keeps the report clicked last when an earlier one\'s thread arrives after it', async () => {
    const alpha = record({ id: 'a', title: 'Alpha report' });
    const beta = record({ id: 'b', title: 'Beta report' });
    vi.spyOn(adminApi, 'feedback').mockResolvedValue({
      feedback: [alpha, beta], total: 2, offset: 0,
      summary: { total: 2, open: 2, bugs: 2, ideas: 0, high_open: 0, awaiting: 0 },
    });
    const first = deferred<FeedbackThread>();
    const second = deferred<FeedbackThread>();
    vi.spyOn(adminApi, 'feedbackThread')
      .mockReturnValueOnce(first.promise)
      .mockReturnValueOnce(second.promise);
    mountWithParams([]);
    await settle();

    const cards = host.querySelectorAll<HTMLButtonElement>('.oa-feedback-card');
    cards[0]!.click();
    cards[1]!.click();
    second.resolve(thread({ feedback: beta }));
    await settle();
    first.resolve(thread({ feedback: alpha }));
    await settle();

    expect(panels.textContent).toContain('Beta report');
    expect(panels.textContent).not.toContain('Alpha report');
  });
});

describe('a reply that fails after the operator moved on', () => {
  it('does not write its error into the report opened after it was sent', async () => {
    vi.spyOn(adminApi, 'feedback').mockResolvedValue({
      feedback: [record({ id: 'a', title: 'Alpha report' }), record({ id: 'b', title: 'Beta report' })],
      total: 2, offset: 0,
      summary: { total: 2, open: 2, bugs: 2, ideas: 0, high_open: 0, awaiting: 0 },
    });
    vi.spyOn(adminApi, 'feedbackThread')
      .mockResolvedValueOnce(thread({ feedback: record({ id: 'a', title: 'Alpha report' }) }))
      .mockResolvedValueOnce(thread({ feedback: record({ id: 'b', title: 'Beta report' }) }));
    let fail!: (failure: unknown) => void;
    vi.spyOn(adminApi, 'replyToFeedback')
      .mockReturnValueOnce(new Promise<never>((_, reject) => { fail = reject; }));
    mountWithParams([]);
    await settle();

    host.querySelectorAll<HTMLButtonElement>('.oa-feedback-card')[0]!.click();
    await settle();
    const area = panels.querySelector('textarea')!;
    area.value = 'Thanks for the report';
    area.dispatchEvent(new Event('input'));
    await settle();
    [...panels.querySelectorAll<HTMLButtonElement>('button')]
      .find((button) => button.textContent?.includes(t('feedbackReplySend')))!
      .click();
    await settle();

    host.querySelectorAll<HTMLButtonElement>('.oa-feedback-card')[1]!.click();
    await settle();
    fail(new ApiError(500, 'boom', 'Reply failed'));
    await settle();

    expect(panels.textContent).toContain('Beta report');
    expect(panels.textContent).not.toContain('Reply failed');
  });
});

describe('the list preview of a long body', () => {
  it('does not leave half an emoji at the end of the cut', async () => {
    // 109 letters and an emoji put the cut on the emoji's first half when it is
    // counted in UTF-16 units.
    const body = `${'a'.repeat(109)}😀b`;
    vi.spyOn(adminApi, 'feedback').mockResolvedValue({
      feedback: [record({ body })], total: 1, offset: 0,
      summary: { total: 1, open: 1, bugs: 1, ideas: 0, high_open: 0, awaiting: 1 },
    });
    mountWithParams([]);
    await settle();

    const text = host.querySelector('.oa-feedback-card-body')?.textContent ?? '';
    expect(text).not.toMatch(/[\uD800-\uDBFF](?![\uDC00-\uDFFF])|(?<![\uD800-\uDBFF])[\uDC00-\uDFFF]/);
    expect(text.endsWith('…')).toBe(true);
  });
});

describe('a thread still loading while a reply for another report ends', () => {
  it('keeps the status and delete controls off until the thread arrives', async () => {
    const alpha = record({ id: 'a', title: 'Alpha report' });
    const beta = record({ id: 'b', title: 'Beta report' });
    vi.spyOn(adminApi, 'feedback').mockResolvedValue({
      feedback: [alpha, beta], total: 2, offset: 0,
      summary: { total: 2, open: 2, bugs: 2, ideas: 0, high_open: 0, awaiting: 0 },
    });
    const betaThread = deferred<FeedbackThread>();
    vi.spyOn(adminApi, 'feedbackThread')
      .mockResolvedValueOnce(thread({ feedback: alpha }))
      .mockReturnValueOnce(betaThread.promise);
    const reply = deferred<FeedbackReply>();
    vi.spyOn(adminApi, 'replyToFeedback').mockReturnValueOnce(reply.promise);
    mountWithParams([]);
    await settle();

    host.querySelectorAll<HTMLButtonElement>('.oa-feedback-card')[0]!.click();
    await settle();
    const area = panels.querySelector('textarea')!;
    area.value = 'Thanks for the report';
    area.dispatchEvent(new Event('input'));
    await settle();
    [...panels.querySelectorAll<HTMLButtonElement>('button')]
      .find((button) => button.textContent?.includes(t('feedbackReplySend')))!
      .click();
    await settle();

    // Beta's thread is now loading, and the reply for alpha is still in flight.
    host.querySelectorAll<HTMLButtonElement>('.oa-feedback-card')[1]!.click();
    await settle();
    reply.resolve({
      id: 'r1', feedback_id: 'a', user_id: 'staff', from_staff: true,
      body: 'Thanks for the report', created_at: Date.now(),
    });
    await settle();

    // The panel's confirm reads "…" while it is busy, so it is found by its
    // place in the footer rather than by its label.
    const confirm = panels.querySelector<HTMLButtonElement>('.oa-panel-foot button.primary');
    expect(confirm).not.toBeNull();
    expect(confirm!.disabled).toBe(true);
    expect(panels.textContent).toContain(t('loading'));

    betaThread.resolve(thread({ feedback: beta }));
    await settle();
    expect(panels.textContent).toContain('Beta report');
  });
});
