import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import { createApp, h, nextTick, shallowRef, type App, type Component } from 'vue';
import { adminApi } from '../src/admin/api';
import { ApiError } from '../src/api/client';
import * as feedbackApi from '../src/api/feedback';
import type { Feedback, FeedbackReply, FeedbackThread } from '../src/api/feedback';
import { providePanelHost } from '../src/composables/usePanelHost';
import { site, siteInfo } from '../src/stores/session';
import { changeLanguage, t } from '../src/composables/useI18n';
import FeedbackPanel from '../src/views/FeedbackPanel.vue';
import AdminFeedback from '../src/views/admin/AdminFeedback.vue';
import { provideAdminView } from '../src/views/admin/adminView';
import { settle } from './helpers';
import { queriesSent } from './admin-query';

vi.mock('vue-router', () => ({ useRouter: () => ({ push: vi.fn() }) }));

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
  document.body.textContent = '';
  vi.restoreAllMocks();
  site.value = null;
});


async function mount(component: Component): Promise<void> {
  app = createApp({
    setup() {
      providePanelHost(shallowRef(panels));
      provideAdminView({ actionsHost: actions, setTitle: () => {}, reload: () => {}, params: [] });
      return () => h(component);
    },
  });
  app.mount(host);
  await settle();
}

function button(root: ParentNode, label: string): HTMLButtonElement {
  const found = [...root.querySelectorAll<HTMLButtonElement>('button')]
    .find((node) => node.textContent?.trim() === label);
  if (!found) throw new Error(`Button not found: ${label}`);
  return found;
}

function type(node: HTMLInputElement | HTMLTextAreaElement, value: string): void {
  node.value = value;
  node.dispatchEvent(new Event('input', { bubbles: true }));
}

function record(over: Partial<Feedback> = {}): Feedback {
  return {
    id: 'f1', user_id: 'u1', kind: 'bug', priority: 'high', status: 'open',
    title: 'Streaming stops', body: 'It stops half way.\n\nEvery time.',
    created_at: Date.now(), updated_at: Date.now(), username: 'reader', nickname: 'Reader',
    replies: 0, author_unread: false, operator_unread: false,
    ...over,
  };
}

function reply(over: Partial<FeedbackReply> = {}): FeedbackReply {
  return {
    id: 'r1', feedback_id: 'f1', user_id: 'admin', from_staff: true,
    body: 'Fixed in **v2026.09.20**.', created_at: Date.now(),
    username: 'operator', ...over,
  };
}

function thread(over: Partial<FeedbackThread> = {}): FeedbackThread {
  return { feedback: record(), replies: [reply()], ...over };
}

describe('the feedback panel somebody writes in', () => {
  it('sends the type, priority, title and body that were chosen', async () => {
    vi.spyOn(feedbackApi, 'listFeedback').mockResolvedValue({ feedback: [], remaining: 10, max_per_day: 10 });
    const send = vi.spyOn(feedbackApi, 'sendFeedback').mockResolvedValue(record());
    await mount(FeedbackPanel);

    button(panels, t('feedbackKindIdea')).click();
    button(panels, t('feedbackPriorityHigh')).click();
    type(panels.querySelector<HTMLInputElement>('input[type="text"]')!, '  A darker terminal  ');
    type(panels.querySelector<HTMLTextAreaElement>('textarea')!, 'It would help at night.');
    await nextTick();

    button(panels, t('feedbackSend')).click();
    await settle();

    expect(send).toHaveBeenCalledWith({
      kind: 'idea', priority: 'high',
      title: 'A darker terminal', body: 'It would help at night.',
    });
    // The confirmation stays up, and the form is empty and ready for another.
    expect(panels.textContent).toContain(t('feedbackSent'));
    expect(panels.querySelector<HTMLTextAreaElement>('textarea')!.value).toBe('');
  });

  it('says which type and priority are chosen, not only by colour', async () => {
    vi.spyOn(feedbackApi, 'listFeedback').mockResolvedValue({ feedback: [], remaining: 10, max_per_day: 10 });
    await mount(FeedbackPanel);
    const pressed = (label: string) => button(panels, label).getAttribute('aria-pressed');

    // The defaults are the bug type at medium priority.
    expect(pressed(t('feedbackKindBug'))).toBe('true');
    expect(pressed(t('feedbackKindIdea'))).toBe('false');
    expect(pressed(t('feedbackPriorityMedium'))).toBe('true');
    expect(pressed(t('feedbackPriorityHigh'))).toBe('false');

    button(panels, t('feedbackKindIdea')).click();
    button(panels, t('feedbackPriorityHigh')).click();
    await nextTick();

    expect(pressed(t('feedbackKindIdea'))).toBe('true');
    expect(pressed(t('feedbackKindBug'))).toBe('false');
    expect(pressed(t('feedbackPriorityHigh'))).toBe('true');
    expect(pressed(t('feedbackPriorityMedium'))).toBe('false');
  });

  it('refuses an empty title or body without asking the server', async () => {
    vi.spyOn(feedbackApi, 'listFeedback').mockResolvedValue({ feedback: [], remaining: 10, max_per_day: 10 });
    const send = vi.spyOn(feedbackApi, 'sendFeedback');
    await mount(FeedbackPanel);

    button(panels, t('feedbackSend')).click();
    await settle();
    expect(send).not.toHaveBeenCalled();
    expect(panels.textContent).toContain(t('feedbackNeedTitle'));

    type(panels.querySelector<HTMLInputElement>('input[type="text"]')!, 'A title');
    button(panels, t('feedbackSend')).click();
    await settle();
    expect(send).not.toHaveBeenCalled();
    expect(panels.textContent).toContain(t('feedbackNeedBody'));
  });

  it('opens one of its own reports, reads the answer and adds to it', async () => {
    const mine = record({ replies: 1, author_unread: true });
    vi.spyOn(feedbackApi, 'listFeedback').mockResolvedValue({
      feedback: [mine], remaining: 9, max_per_day: 10,
    });
    const read = vi.spyOn(feedbackApi, 'fetchThread').mockResolvedValue(thread({ feedback: mine }));
    const answer = vi.spyOn(feedbackApi, 'replyToFeedback')
      .mockResolvedValue(reply({ id: 'r2', from_staff: false, body: 'still happening' }));
    vi.spyOn(feedbackApi, 'fetchFeedbackUnread').mockResolvedValue({ unread: 0 });
    await mount(FeedbackPanel);

    // An unanswered report is marked in the list it sits in.
    expect(panels.querySelector('.oa-feedback-item .oa-menu-unread')).not.toBeNull();

    panels.querySelector<HTMLButtonElement>('.oa-feedback-item')!.click();
    await settle();
    expect(read).toHaveBeenCalledWith('f1');
    expect(panels.querySelectorAll('.oa-thread-turn')).toHaveLength(2);
    expect(panels.textContent).toContain(t('feedbackFromStaff'));
    expect(panels.querySelector('.oa-thread-turn.staff strong')?.textContent).toBe('v2026.09.20');

    type(panels.querySelector<HTMLTextAreaElement>('.oa-feedback-answer textarea')!, 'still happening');
    await settle();
    button(panels, t('feedbackReplySend')).click();
    await settle();
    // No challenge switched on here, so it goes straight out with no token.
    expect(answer).toHaveBeenCalledWith('f1', 'still happening', '');
    expect(panels.querySelectorAll('.oa-thread-turn')).toHaveLength(3);
  });

  // Whether the name arrives at all is the server's decision; the panel draws
  // what it was sent, and reads as staff either way.
  it('signs a staff reply with the name when the server sent one', async () => {
    const mine = record({ replies: 1 });
    vi.spyOn(feedbackApi, 'listFeedback').mockResolvedValue({
      feedback: [mine], remaining: 9, max_per_day: 10,
    });
    vi.spyOn(feedbackApi, 'fetchFeedbackUnread').mockResolvedValue({ unread: 0 });
    const read = vi.spyOn(feedbackApi, 'fetchThread').mockResolvedValue(thread({
      feedback: mine, replies: [reply({ username: 'onyx', nickname: '黑曜' })],
    }));
    await mount(FeedbackPanel);
    panels.querySelector<HTMLButtonElement>('.oa-feedback-item')!.click();
    await settle();
    expect(panels.querySelector('.oa-thread-turn.staff .oa-thread-who')!.textContent)
      .toContain(`${t('feedbackFromStaff')} · 黑曜`);

    // Switched off upstream: no name in the payload, and none on screen.
    // The keys are absent, not empty: that is what the server sends once the
    // switch is off.
    const { username: _u, nickname: _n, ...anonymous } = reply();
    read.mockResolvedValue(thread({ feedback: mine, replies: [anonymous] }));
    // The panel's back arrow is an icon, so it is found by its name rather
    // than by its text.
    panels.querySelector<HTMLButtonElement>(`button[aria-label="${t('back')}"]`)!.click();
    await settle();
    panels.querySelector<HTMLButtonElement>('.oa-feedback-item')!.click();
    await settle();
    const who = panels.querySelector('.oa-thread-turn.staff .oa-thread-who')!.textContent ?? '';
    expect(who).toContain(t('feedbackFromStaff'));
    expect(who).not.toContain('·');
  });

  it('shows what this account has already sent, with the status it was given', async () => {
    vi.spyOn(feedbackApi, 'listFeedback').mockResolvedValue({
      feedback: [record({ status: 'resolved', title: 'Already reported' })],
      remaining: 9, max_per_day: 10,
    });
    await mount(FeedbackPanel);

    expect(panels.textContent).toContain('Already reported');
    expect(panels.textContent).toContain(t('feedbackStatusResolved'));
    expect(panels.textContent).toContain(t('feedbackRemaining', { count: 9 }));
  });

  it('will not take a body longer than the server keeps', async () => {
    vi.spyOn(feedbackApi, 'listFeedback').mockResolvedValue({ feedback: [], remaining: 10, max_per_day: 10 });
    await mount(FeedbackPanel);

    expect(panels.querySelector('textarea')!.getAttribute('maxlength')).toBe('8000');
  });

  it('shows the report opened last, whichever thread arrives first', async () => {
    const reports = [record(), record({ id: 'f2', title: 'Second report', body: 'Second body' })];
    vi.spyOn(feedbackApi, 'listFeedback').mockResolvedValue({ feedback: reports, remaining: 9, max_per_day: 10 });
    vi.spyOn(feedbackApi, 'fetchFeedbackUnread').mockResolvedValue({ unread: 0 });
    let releaseFirst!: (value: FeedbackThread) => void;
    const firstThread = new Promise<FeedbackThread>((resolve) => { releaseFirst = resolve; });
    let releaseSecond!: (value: FeedbackThread) => void;
    const secondThread = new Promise<FeedbackThread>((resolve) => { releaseSecond = resolve; });
    vi.spyOn(feedbackApi, 'fetchThread')
      .mockReturnValueOnce(firstThread)
      .mockReturnValueOnce(secondThread);
    await mount(FeedbackPanel);

    const items = panels.querySelectorAll<HTMLButtonElement>('.oa-feedback-item');
    items[0]!.click();
    items[1]!.click();
    releaseSecond(thread({ feedback: reports[1]! }));
    await settle();
    releaseFirst(thread({ feedback: reports[0]! }));
    await settle();

    expect(panels.querySelector('.oa-thread-body')?.textContent).toContain('Second body');
    expect(panels.querySelector('.oa-thread-body')?.textContent).not.toContain('It stops half way');
  });

  /**
   * Replies to the first report, leaves that reply in flight, goes back and
   * opens the second, and types a draft for it. The returned function finishes
   * the first reply with the given outcome.
   */
  async function replyToFirstThenOpenSecond(): Promise<(outcome: FeedbackReply | ApiError) => Promise<void>> {
    const reports = [record(), record({ id: 'f2', title: 'Second report', body: 'Second body' })];
    vi.spyOn(feedbackApi, 'listFeedback').mockResolvedValue({ feedback: reports, remaining: 9, max_per_day: 10 });
    vi.spyOn(feedbackApi, 'fetchFeedbackUnread').mockResolvedValue({ unread: 0 });
    let releaseSecond!: (value: FeedbackThread) => void;
    const secondThread = new Promise<FeedbackThread>((resolve) => { releaseSecond = resolve; });
    vi.spyOn(feedbackApi, 'fetchThread')
      .mockResolvedValueOnce(thread({ feedback: reports[0]! }))
      .mockReturnValueOnce(secondThread);
    let finishReply!: (outcome: FeedbackReply | ApiError) => void;
    const replyInFlight = new Promise<FeedbackReply>((resolve, reject) => {
      finishReply = (outcome) => (outcome instanceof ApiError ? reject(outcome) : resolve(outcome));
    });
    vi.spyOn(feedbackApi, 'replyToFeedback').mockReturnValueOnce(replyInFlight);
    await mount(FeedbackPanel);

    panels.querySelector<HTMLButtonElement>('.oa-feedback-item')!.click();
    await settle();
    type(panels.querySelector<HTMLTextAreaElement>('.oa-feedback-answer textarea')!, 'answer for the first');
    await settle();
    button(panels, t('feedbackReplySend')).click();
    await settle();

    panels.querySelector<HTMLButtonElement>(`button[aria-label="${t('back')}"]`)!.click();
    await settle();
    panels.querySelectorAll<HTMLButtonElement>('.oa-feedback-item')[1]!.click();
    releaseSecond(thread({ feedback: reports[1]! }));
    await settle();
    type(panels.querySelector<HTMLTextAreaElement>('.oa-feedback-answer textarea')!, 'draft for the second');
    await settle();

    return async (outcome) => {
      finishReply(outcome);
      await settle();
    };
  }

  // The reply was written for the first report. Its success must not clear the
  // draft the reader has started on the second.
  it('keeps the draft for another report when an earlier reply is sent', async () => {
    const finish = await replyToFirstThenOpenSecond();
    await finish(reply({ id: 'r2', from_staff: false, body: 'answer for the first' }));

    expect(panels.querySelector<HTMLTextAreaElement>('.oa-feedback-answer textarea')!.value)
      .toBe('draft for the second');
    expect(panels.querySelector('.oa-thread-body')?.textContent).toContain('Second body');
  });

  // Likewise its failure: the error belongs to the first report, not to the one
  // now on screen.
  it('does not show an earlier reply failure on another report', async () => {
    const finish = await replyToFirstThenOpenSecond();
    await finish(new ApiError(409, 'feedback_closed', 'This report is closed'));

    expect(panels.textContent).not.toContain('This report is closed');
    expect(panels.querySelector<HTMLTextAreaElement>('.oa-feedback-answer textarea')!.value)
      .toBe('draft for the second');
  });

  // Off unless the operator switched it on: an account that is already
  // signed in and already capped does not meet a challenge by default.
  it('sends straight away when no challenge is switched on', async () => {
    vi.spyOn(feedbackApi, 'listFeedback').mockResolvedValue({ feedback: [], remaining: 10, max_per_day: 10 });
    const send = vi.spyOn(feedbackApi, 'sendFeedback').mockResolvedValue(record());
    await mount(FeedbackPanel);

    type(panels.querySelector<HTMLInputElement>('input[type="text"]')!, 'A title');
    type(panels.querySelector<HTMLTextAreaElement>('textarea')!, 'Some detail.');
    button(panels, t('feedbackSend')).click();
    await settle();

    expect(document.querySelector('.oa-challenge')).toBeNull();
    expect(send).toHaveBeenCalledOnce();
  });

  // The widget is a fixed-width box with its own chrome, so it gets a sheet
  // of its own rather than a row in a panel that resizes down to 320px — the
  // same answer the redemption dialog already gives.
  it('holds the report behind a sheet when the challenge is switched on', async () => {
    site.value = { ...siteInfo.value, turnstile_on_feedback: true, turnstile_site_key: '1x000' };
    vi.spyOn(feedbackApi, 'listFeedback').mockResolvedValue({ feedback: [], remaining: 10, max_per_day: 10 });
    const send = vi.spyOn(feedbackApi, 'sendFeedback').mockResolvedValue(record());
    await mount(FeedbackPanel);

    expect(document.querySelector('.oa-challenge')).toBeNull();
    type(panels.querySelector<HTMLInputElement>('input[type="text"]')!, 'A title');
    type(panels.querySelector<HTMLTextAreaElement>('textarea')!, 'Some detail.');
    button(panels, t('feedbackSend')).click();
    await settle();

    // Nothing is sent until the check is passed, and what was written is
    // still there waiting for it.
    expect(send).not.toHaveBeenCalled();
    const sheet = document.querySelector('.oa-modal-overlay');
    expect(sheet).not.toBeNull();
    expect(sheet!.textContent).toContain(t('feedbackChallengeTitle'));
    expect(sheet!.querySelector('.oa-challenge')).not.toBeNull();
    expect(panels.querySelector<HTMLTextAreaElement>('textarea')!.value).toBe('Some detail.');
  });

  // The gate the operator switched on covers both writes. A thread holds
  // fifty turns against ten reports a day, so replying is the cheaper thing
  // to automate — and the one a check on the form alone would have missed.
  it('holds a reply behind the same sheet the report goes through', async () => {
    site.value = { ...siteInfo.value, turnstile_on_feedback: true, turnstile_site_key: '1x000' };
    const mine = record({ replies: 1 });
    vi.spyOn(feedbackApi, 'listFeedback').mockResolvedValue({
      feedback: [mine], remaining: 9, max_per_day: 10,
    });
    vi.spyOn(feedbackApi, 'fetchThread').mockResolvedValue(thread({ feedback: mine }));
    vi.spyOn(feedbackApi, 'fetchFeedbackUnread').mockResolvedValue({ unread: 0 });
    const answer = vi.spyOn(feedbackApi, 'replyToFeedback')
      .mockResolvedValue(reply({ id: 'r2', from_staff: false, body: 'still happening' }));
    await mount(FeedbackPanel);

    panels.querySelector<HTMLButtonElement>('.oa-feedback-item')!.click();
    await settle();
    type(panels.querySelector<HTMLTextAreaElement>('.oa-feedback-answer textarea')!, 'still happening');
    await settle();
    button(panels, t('feedbackReplySend')).click();
    await settle();

    // Nothing is sent until the check is passed, and the sheet says which of
    // the two writes it is holding.
    expect(answer).not.toHaveBeenCalled();
    const sheet = document.querySelector('.oa-modal-overlay');
    expect(sheet).not.toBeNull();
    expect(sheet!.textContent).toContain(t('feedbackReplyChallengeTitle'));
    expect(sheet!.querySelector('.oa-challenge')).not.toBeNull();
    // And what was written is still in the box, waiting for it.
    expect(panels.querySelector<HTMLTextAreaElement>('.oa-feedback-answer textarea')!.value)
      .toBe('still happening');
  });

  // The server refuses past the cap either way; the point of asking first is
  // that nobody types five hundred words into a box that cannot be sent.
  it('withdraws the send button once the day is used up', async () => {
    vi.spyOn(feedbackApi, 'listFeedback').mockResolvedValue({ feedback: [], remaining: 0, max_per_day: 10 });
    await mount(FeedbackPanel);

    expect(panels.textContent).toContain(t('feedbackNoneLeft'));
    expect(() => button(panels, t('feedbackSend'))).toThrow();
  });
});

describe('the operator’s feedback page', () => {
  const summary = { total: 2, open: 1, bugs: 1, ideas: 1, high_open: 1, awaiting: 1 };

  function list(rows: Feedback[]) {
    return vi.spyOn(adminApi, 'feedback').mockResolvedValue({
      feedback: rows, total: rows.length, offset: 0, summary,
    });
  }

  it('lists reports with their author and counts what is still waiting', async () => {
    list([record(), record({ id: 'f2', kind: 'idea', priority: 'low', status: 'resolved', title: 'A darker theme' })]);
    await mount(AdminFeedback);

    const cards = host.querySelectorAll('.oa-feedback-card');
    expect(cards).toHaveLength(2);
    expect(cards[0]!.textContent).toContain('Streaming stops');
    expect(cards[0]!.textContent).toContain(t('feedbackFrom', { name: 'Reader' }));
    expect(cards[1]!.classList.contains('resolved')).toBe(true);
    expect(host.querySelector('.oa-stat-grid')!.textContent).toContain(t('feedbackStatHigh'));
  });

  it('opens one report whole and resolves it without editing what was written', async () => {
    list([record()]);
    vi.spyOn(adminApi, 'feedbackThread').mockResolvedValue(thread({ replies: [] }));
    const patch = vi.spyOn(adminApi, 'setFeedbackStatus').mockResolvedValue(record({ status: 'resolved' }));
    await mount(AdminFeedback);

    host.querySelector<HTMLButtonElement>('.oa-feedback-card')!.click();
    await settle();
    // Both paragraphs survive the Markdown pass: the blank line in the middle
    // is half of what was said.
    const body = panels.querySelector('.oa-thread-turn .oa-thread-body')!;
    expect(body.textContent).toContain('It stops half way.');
    expect(body.textContent).toContain('Every time.');

    button(panels, t('feedbackResolve')).click();
    await settle();
    expect(patch).toHaveBeenCalledWith('f1', 'resolved');
  });

  it('renders both sides of the conversation as Markdown and answers it', async () => {
    list([record({ replies: 1, operator_unread: true })]);
    vi.spyOn(adminApi, 'feedbackThread').mockResolvedValue(thread());
    const send = vi.spyOn(adminApi, 'replyToFeedback').mockResolvedValue(reply({ id: 'r2', body: 'and again' }));
    await mount(AdminFeedback);

    // The card says who spoke last, which is the one thing here to act on.
    expect(host.querySelector('.oa-feedback-card')!.textContent).toContain(t('feedbackAwaiting'));

    host.querySelector<HTMLButtonElement>('.oa-feedback-card')!.click();
    await settle();

    const turns = panels.querySelectorAll('.oa-thread-turn');
    expect(turns).toHaveLength(2);
    expect(turns[1]!.classList.contains('staff')).toBe(true);
    // Rendered, not printed: the asterisks became an element, and no step
    // anywhere assembled an HTML string to get there.
    expect(turns[1]!.querySelector('strong')?.textContent).toBe('v2026.09.20');

    const box = panels.querySelector<HTMLTextAreaElement>('.oa-feedback-answer textarea')!;
    type(box, 'and again');
    await settle();
    button(panels, t('feedbackReplySend')).click();
    await settle();
    expect(send).toHaveBeenCalledWith('f1', 'and again');
    expect(box.value).toBe('');
  });

  // A report somebody else wrote is rendered in the operator's own screen, so
  // the renderer's two narrowings are what this screen leans on.
  it('never draws an image or a javascript: link out of a stranger\'s report', async () => {
    list([record()]);
    vi.spyOn(adminApi, 'feedbackThread').mockResolvedValue(thread({
      replies: [reply({
        from_staff: false,
        body: '![](https://tracker.example/x.png) and [click](javascript:alert(1))',
      })],
    }));
    await mount(AdminFeedback);
    host.querySelector<HTMLButtonElement>('.oa-feedback-card')!.click();
    await settle();

    expect(panels.querySelectorAll('img')).toHaveLength(0);
    for (const anchor of panels.querySelectorAll('a')) {
      expect(anchor.getAttribute('href') ?? '').not.toContain('javascript:');
    }
  });

  it('narrows the list by status without leaving the page it was on', async () => {
    const reads = list([record()]);
    await mount(AdminFeedback);
    reads.mockClear();

    const statusSelect = [...host.querySelectorAll<HTMLElement>('.oa-field')]
      .find((field) => field.querySelector('.oa-field-label')?.textContent === t('colStatus'))!;
    statusSelect.querySelector<HTMLButtonElement>('.oa-select')!.click();
    await settle();
    [...document.querySelectorAll<HTMLElement>('[role="option"]')]
      .find((item) => item.textContent?.trim() === t('feedbackStatusOpen'))!.click();
    await settle();

    expect(queriesSent(reads).some((query) => query.includes('status=open'))).toBe(true);
  });

  function deferred<T>(): { promise: Promise<T>; resolve: (value: T) => void } {
    let resolve!: (value: T) => void;
    const promise = new Promise<T>((done) => { resolve = done; });
    return { promise, resolve };
  }

  it('shows the report that was clicked last, whichever thread arrives first', async () => {
    list([record(), record({ id: 'f2', title: 'Second report' })]);
    const first = deferred<FeedbackThread>();
    const second = deferred<FeedbackThread>();
    vi.spyOn(adminApi, 'feedbackThread')
      .mockReturnValueOnce(first.promise)
      .mockReturnValueOnce(second.promise);
    await mount(AdminFeedback);

    const cards = host.querySelectorAll<HTMLButtonElement>('.oa-feedback-card');
    cards[0]!.click();
    cards[1]!.click();
    second.resolve(thread({ feedback: record({ id: 'f2', title: 'Second report' }) }));
    await settle();
    first.resolve(thread({ feedback: record({ title: 'First report' }) }));
    await settle();

    expect(panels.textContent).toContain('Second report');
    expect(panels.textContent).not.toContain('First report');
  });

  it('does not reopen a report closed while its thread was still loading', async () => {
    list([record()]);
    const pending = deferred<FeedbackThread>();
    vi.spyOn(adminApi, 'feedbackThread').mockReturnValueOnce(pending.promise);
    await mount(AdminFeedback);

    host.querySelector<HTMLButtonElement>('.oa-feedback-card')!.click();
    await settle();
    panels.querySelector<HTMLButtonElement>('.oa-panel-head .oa-icon-btn')!.click();
    // OaPanel tells the page to close only after its exit animation, so wait for
    // the panel to leave the DOM rather than for a fixed delay that tracks the animation.
    await vi.waitFor(() => expect(panels.querySelector('.oa-panel')).toBeNull());
    await settle();
    expect(panels.querySelector('.oa-panel')).toBeNull();

    pending.resolve(thread({ replies: [] }));
    await settle();
    expect(panels.querySelector('.oa-panel')).toBeNull();
  });

  it('lowers the awaiting count only when the opened thread was waiting', async () => {
    const counts = { total: 2, open: 2, bugs: 2, ideas: 0, high_open: 0, awaiting: 1 };
    vi.spyOn(adminApi, 'feedback').mockResolvedValue({
      feedback: [record(), record({ id: 'f2', title: 'Second report', operator_unread: true })],
      total: 2, offset: 0, summary: counts,
    });
    vi.spyOn(adminApi, 'feedbackThread').mockImplementation(async (id) =>
      thread({ feedback: record({ id, operator_unread: id === 'f2' }) }));
    await mount(AdminFeedback);

    const awaiting = () => [...host.querySelectorAll<HTMLElement>('.oa-stat')]
      .find((stat) => stat.querySelector('.oa-stat-label')?.textContent === t('feedbackStatAwaiting'))!
      .querySelector('.oa-stat-value')!.textContent;
    const cards = host.querySelectorAll<HTMLButtonElement>('.oa-feedback-card');

    // An answered thread was not waiting, so opening it changes nothing.
    cards[0]!.click();
    await settle();
    expect(awaiting()).toBe('1');

    cards[1]!.click();
    await settle();
    expect(awaiting()).toBe('0');

    // Reopening the same thread is not a second answer to a question.
    cards[1]!.click();
    await settle();
    expect(awaiting()).toBe('0');
  });

  it('redraws the heading in the new language, not only the text it was set with', async () => {
    list([]);
    const heading = shallowRef<() => string>(() => '');
    app = createApp({
      setup() {
        providePanelHost(shallowRef(panels));
        provideAdminView({
          actionsHost: actions,
          setTitle: (title) => { heading.value = title; },
          reload: () => {},
          params: [],
        });
        // What the shell does: draw the page's heading through its getter.
        return () => h('div', [h('h1', heading.value()), h(AdminFeedback)]);
      },
    });
    app.mount(host);
    await settle();
    const english = t('navFeedback');
    expect(host.querySelector('h1')!.textContent).toBe(english);

    await changeLanguage('zh');
    await settle();
    expect(t('navFeedback')).not.toBe(english);
    expect(host.querySelector('h1')!.textContent).toBe(t('navFeedback'));
  });
});
