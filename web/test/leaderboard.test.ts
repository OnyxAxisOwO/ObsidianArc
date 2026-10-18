// The leaderboard as readers and operators meet it.
//
// Three things are worth a test here and they are all about disclosure rather
// than layout: the entry only exists where the operator put it, the panel
// never invents a name the server withheld, and changing the window or the
// measure asks the server again rather than re-sorting what is on screen —
// because both are aggregates the browser does not hold.

import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import { createApp, h, nextTick, ref, type App } from 'vue';
import { createMemoryHistory, createRouter } from 'vue-router';
import { api } from '@/api/client';
import type { Account } from '@/api/auth';
import type { Leaderboard } from '@/api/leaderboard';
import { providePanelHost } from '@/composables/usePanelHost';
import { changeLanguage, t } from '@/composables/useI18n';
import AccountMenu from '@/layouts/AccountMenu.vue';
import { adopt, forget, site } from '@/stores/session';
import LeaderboardPanel from '@/views/LeaderboardPanel.vue';
import { makeAccount } from './fixtures/account';

vi.mock('@/stores/feedback', () => ({
  feedbackUnread: ref(0),
  refreshFeedbackUnread: vi.fn(async () => {}),
  forgetFeedbackUnread: vi.fn(),
}));

const ACCOUNT = makeAccount({
  id: 'me', username: 'member', nickname: 'Member', email: '', group_id: 'g', created_at: 0, updated_at: 0,
});

/** A board with two named places, the reader second. */
function board(extra: Partial<Leaderboard> = {}): Leaderboard {
  return {
    period: 'week',
    metric: 'tokens',
    identity: 'nickname',
    accounts: [
      { rank: 1, name: 'Otter', avatar: '', value: 5000, requests: 40, tokens: 5000, models: 2 },
      { rank: 2, name: 'Member', avatar: '', value: 1200, requests: 9, tokens: 1200, models: 1, self: true },
    ],
    me: { rank: 2, value: 1200, gap: 3800, participants: 2 },
    models: [{ rank: 1, name: 'Sonnet', value: 4000, users: 2, requests: 30, tokens: 4000 }],
    ...extra,
  };
}

let app: App | null = null;
let host: HTMLElement;
let panelHost: HTMLElement;

beforeEach(async () => {
  await changeLanguage('en');
  host = document.createElement('div');
  panelHost = document.createElement('div');
  document.body.append(host, panelHost);
});

afterEach(async () => {
  app?.unmount();
  app = null;
  site.value = null;
  forget();
  vi.restoreAllMocks();
  await changeLanguage('en');
});

async function mountPanel(): Promise<void> {
  const router = createRouter({
    history: createMemoryHistory(),
    routes: [{ path: '/:p(.*)*', component: { render: () => null } }],
  });
  await router.push('/leaderboard');
  await router.isReady();
  app = createApp({
    setup() {
      providePanelHost(ref(panelHost));
      return () => h(LeaderboardPanel);
    },
  });
  app.use(router).mount(host);
  await nextTick();
  await nextTick();
}

describe('the leaderboard panel', () => {
  it('shows the reader their own place and how far off the top they are', async () => {
    vi.spyOn(api, 'get').mockResolvedValue(board());
    await mountPanel();

    expect(api.get).toHaveBeenCalledWith('/api/leaderboard?period=week&metric=tokens', expect.objectContaining({ signal: expect.any(AbortSignal) }));
    const text = panelHost.textContent ?? '';
    expect(text).toContain(t('boardYourRank', { total: 2 }));
    expect(text).toContain(t('boardBehind', { amount: '3.8k' }));
    // The reader's own row is marked for the page, not merely present.
    expect(panelHost.querySelector('.oa-board-row.self .oa-board-name')?.textContent).toContain('Member');
    // Both places in this fixture are on the podium; the class is what the
    // stylesheet reads to give the top three their medal.
    expect(panelHost.querySelectorAll('.oa-board-row.podium')).toHaveLength(2);
    // The models board is the server's to include.
    expect(text).toContain('Sonnet');
    expect(text).toContain(t('boardUsersCount', { count: 2 }));
  });

  it('keeps tied anonymous rows distinct when the board refreshes', async () => {
    const get = vi.spyOn(api, 'get').mockResolvedValueOnce(board()).mockResolvedValueOnce(board({
      identity: 'anonymous',
      accounts: [
        { rank: 1, value: 5000, requests: 40, tokens: 5000, models: 2 },
        { rank: 1, value: 5000, requests: 39, tokens: 5000, models: 2 },
      ],
    }));
    const warn = vi.spyOn(console, 'warn').mockImplementation(() => {});
    await mountPanel();
    warn.mockClear();

    // Equal values share a rank, and anonymous rows have no handle or name
    // to complete it. Changing the window patches the existing list with
    // those tied rows, which is where duplicate Vue keys used to appear.
    panelHost.querySelectorAll('.oa-segment')[0]!.querySelectorAll('button')[2]!.click();
    await new Promise((resolve) => setTimeout(resolve, 0));
    await nextTick();

    expect(get).toHaveBeenLastCalledWith('/api/leaderboard?period=month&metric=tokens', expect.objectContaining({ signal: expect.any(AbortSignal) }));
    expect(panelHost.querySelectorAll('.oa-board-list')[0]!.querySelectorAll('.oa-board-row')).toHaveLength(2);
    expect(warn.mock.calls.flat().join('\n')).not.toContain('Duplicate keys found during update');
  });

  it('asks the server for each window and each measure, since the board is ranked and cut short per measure', async () => {
    const initialBoard = board({
      accounts: [
        { rank: 1, name: 'Otter', avatar: '', value: 5000, requests: 40, tokens: 5000, models: 2 },
        { rank: 2, name: 'Member', avatar: '', value: 1200, requests: 90, tokens: 1200, models: 1, self: true },
      ],
      models: [
        { rank: 1, name: 'Sonnet', value: 4000, users: 2, requests: 30, tokens: 4000 },
        { rank: 2, name: 'Haiku', value: 1000, users: 1, requests: 50, tokens: 1000 },
      ],
    });
    // Listed against the measure on purpose: Otter (40) before Member (90), and
    // Sonnet (30) before Haiku (50). A client that re-sorted would put Member and
    // Haiku first, so these rows show the order the server sent is the one drawn.
    const requestsBoard = board({
      metric: 'requests',
      accounts: [
        { rank: 1, name: 'Otter', avatar: '', value: 40, requests: 40, tokens: 5000, models: 2 },
        { rank: 2, name: 'Member', avatar: '', value: 90, requests: 90, tokens: 1200, models: 1, self: true },
      ],
      models: [
        { rank: 1, name: 'Sonnet', value: 30, users: 2, requests: 30, tokens: 4000 },
        { rank: 2, name: 'Haiku', value: 50, users: 1, requests: 50, tokens: 1000 },
      ],
    });
    const get = vi.spyOn(api, 'get').mockImplementation(async (url) =>
      url.includes('metric=requests') ? requestsBoard : initialBoard);
    await mountPanel();
    get.mockClear();

    const segments = panelHost.querySelectorAll('.oa-segment');
    // Switching period to month asks the server again with AbortSignal
    segments[0]!.querySelectorAll('button')[2]!.click();
    await new Promise((resolve) => setTimeout(resolve, 0));
    await nextTick();
    expect(get).toHaveBeenCalledTimes(1);
    expect(get).toHaveBeenCalledWith('/api/leaderboard?period=month&metric=tokens', expect.objectContaining({ signal: expect.any(AbortSignal) }));

    // Switching metric to requests asks for the requests board, not a re-sort of the tokens one
    get.mockClear();
    segments[1]!.querySelectorAll('button')[1]!.click();
    await new Promise((resolve) => setTimeout(resolve, 0));
    await nextTick();
    expect(get).toHaveBeenCalledTimes(1);
    expect(get).toHaveBeenCalledWith('/api/leaderboard?period=month&metric=requests', expect.objectContaining({ signal: expect.any(AbortSignal) }));

    // The rows follow the array the server sent, not a client re-sort by requests.
    const accountRows = panelHost.querySelectorAll('.oa-board-list')[0]!.querySelectorAll('.oa-board-row');
    expect(accountRows[0]!.querySelector('.oa-board-name')?.textContent).toContain('Otter');
    expect(accountRows[0]!.querySelector('.oa-board-value')?.textContent).toBe('40');
    expect(accountRows[1]!.querySelector('.oa-board-name')?.textContent).toContain('Member');
    expect(accountRows[1]!.querySelector('.oa-board-value')?.textContent).toBe('90');

    // Models follow the same rule: Sonnet is listed first and is drawn first.
    const modelRows = panelHost.querySelectorAll('.oa-board-list')[1]!.querySelectorAll('.oa-board-row');
    expect(modelRows[0]!.querySelector('.oa-board-name')?.textContent).toContain('Sonnet');
    expect(modelRows[1]!.querySelector('.oa-board-name')?.textContent).toContain('Haiku');

    // Switching back to tokens asks again, and the list comes back in tokens order
    get.mockClear();
    segments[1]!.querySelectorAll('button')[0]!.click();
    await new Promise((resolve) => setTimeout(resolve, 0));
    await nextTick();
    expect(get).toHaveBeenCalledTimes(1);
    expect(get).toHaveBeenCalledWith('/api/leaderboard?period=month&metric=tokens', expect.objectContaining({ signal: expect.any(AbortSignal) }));

    // Accounts back to Otter (5000) > Member (1200)
    const accountRowsBack = panelHost.querySelectorAll('.oa-board-list')[0]!.querySelectorAll('.oa-board-row');
    expect(accountRowsBack[0]!.querySelector('.oa-board-name')?.textContent).toContain('Otter');
    expect(accountRowsBack[0]!.querySelector('.oa-board-value')?.textContent).toBe('5k');
  });

  it('pins the reader\'s standing for the measure in view when they are outside the visible list', async () => {
    // Outside the top of the tokens list: the reader is 30th by tokens and
    // 9th by requests, and only the requests board lists Rival.
    const tokensBoard = board({
      accounts: [{ rank: 1, name: 'Otter', avatar: '', value: 5000, requests: 40, tokens: 5000, models: 2 }],
      me: { rank: 30, value: 1200, gap: 3800, participants: 40 },
    });
    const requestsBoard = board({
      metric: 'requests',
      accounts: [
        { rank: 1, name: 'Rival', avatar: '', value: 120, requests: 120, tokens: 300, models: 1 },
        { rank: 2, name: 'Otter', avatar: '', value: 40, requests: 40, tokens: 5000, models: 2 },
      ],
      me: { rank: 9, value: 9, gap: 31, participants: 40 },
    });
    vi.spyOn(api, 'get').mockImplementation(async (url) =>
      url.includes('metric=requests') ? requestsBoard : tokensBoard);
    await mountPanel();

    panelHost.querySelectorAll('.oa-segment')[1]!.querySelectorAll('button')[1]!.click();
    await new Promise((resolve) => setTimeout(resolve, 0));
    await nextTick();

    // The pinned card carries the requests place and its own count, not the
    // tokens place relabelled as requests.
    expect(panelHost.querySelector('.oa-board-me-rank')?.textContent?.trim()).toBe('#9');
    expect(panelHost.querySelector('.oa-board-me .oa-field-hint')?.textContent).toContain(t('boardRequests', { count: '9' }));
    expect(panelHost.querySelectorAll('.oa-board-list')[0]!.textContent).toContain('Rival');
  });

  it('keeps the unit of the board on screen while the other measure is loading', async () => {
    let resolveRequests!: (value: Leaderboard) => void;
    const requestsPending = new Promise<Leaderboard>((resolve) => { resolveRequests = resolve; });
    vi.spyOn(api, 'get').mockImplementation(async (url) =>
      url.includes('metric=requests') ? requestsPending : board());
    await mountPanel();

    panelHost.querySelectorAll('.oa-segment')[1]!.querySelectorAll('button')[1]!.click();
    await nextTick();

    // The tokens board is still what is shown, so its 1.2k is tokens, not requests.
    expect(panelHost.querySelector('.oa-board-me .oa-field-hint')?.textContent).toContain(t('boardTokens', { count: '1.2k' }));

    // The server's requests board: its values are requests, which is what the
    // pinned figure must show. The client no longer derives them.
    resolveRequests(board({
      metric: 'requests',
      accounts: [
        { rank: 1, name: 'Otter', avatar: '', value: 40, requests: 40, tokens: 5000, models: 2 },
        { rank: 2, name: 'Member', avatar: '', value: 9, requests: 9, tokens: 1200, models: 1, self: true },
      ],
      me: { rank: 2, value: 9, gap: 31, participants: 2 },
    }));
    await new Promise((resolve) => setTimeout(resolve, 0));
    await nextTick();

    // Once the requests board lands, the pinned figure is the reader's requests
    // (9, from the self row), not the tokens 1.2k that the placeholder carried.
    expect(panelHost.querySelector('.oa-board-me .oa-field-hint')?.textContent).toContain(t('boardRequests', { count: '9' }));
  });

  it('aborts the previous request when period changes quickly', async () => {
    let resolveSecond!: (value: Leaderboard) => void;
    const secondPromise = new Promise<Leaderboard>((resolve) => { resolveSecond = resolve; });
    const abortSignals: AbortSignal[] = [];
    vi.spyOn(api, 'get').mockImplementation(async (_url, options) => {
      if (options?.signal) abortSignals.push(options.signal);
      if (abortSignals.length === 2) return secondPromise;
      return board();
    });
    await mountPanel();
    expect(abortSignals).toHaveLength(1);
    expect(abortSignals[0]?.aborted).toBe(false);

    const segments = panelHost.querySelectorAll('.oa-segment');
    segments[0]!.querySelectorAll('button')[2]!.click(); // switch to month
    await nextTick();
    expect(abortSignals).toHaveLength(2);
    expect(abortSignals[1]?.aborted).toBe(false);

    segments[0]!.querySelectorAll('button')[0]!.click(); // quickly switch to day
    await nextTick();
    expect(abortSignals[1]?.aborted).toBe(true);
    expect(abortSignals).toHaveLength(3);
    expect(abortSignals[2]?.aborted).toBe(false);

    resolveSecond(board());
  });

  it('aborts the in-flight period request when metric is toggled during loading', async () => {
    let resolvePending!: (value: Leaderboard) => void;
    const pendingPromise = new Promise<Leaderboard>((resolve) => { resolvePending = resolve; });
    const abortSignals: AbortSignal[] = [];
    const calledUrls: string[] = [];

    vi.spyOn(api, 'get').mockImplementation(async (url, options) => {
      calledUrls.push(url);
      if (options?.signal) abortSignals.push(options.signal);
      if (calledUrls.length === 2) return pendingPromise;
      return board();
    });

    await mountPanel();
    expect(calledUrls).toEqual(['/api/leaderboard?period=week&metric=tokens']);
    expect(abortSignals[0]?.aborted).toBe(false);

    // Switch period to month -> triggers in-flight request 2
    const segments = panelHost.querySelectorAll('.oa-segment');
    segments[0]!.querySelectorAll('button')[2]!.click();
    await nextTick();
    expect(calledUrls).toHaveLength(2);
    expect(calledUrls[1]).toBe('/api/leaderboard?period=month&metric=tokens');
    expect(abortSignals[1]?.aborted).toBe(false);

    // While month request is in-flight, switch metric to requests
    segments[1]!.querySelectorAll('button')[1]!.click();
    await nextTick();
    // Month & tokens request should have been aborted, and month & requests requested
    expect(abortSignals[1]?.aborted).toBe(true);
    expect(calledUrls).toHaveLength(3);
    expect(calledUrls[2]).toBe('/api/leaderboard?period=month&metric=requests');
    expect(abortSignals[2]?.aborted).toBe(false);

    resolvePending(board());
  });

  it('differentiates model ranks when values match but user counts differ', async () => {
    const tiedBoard = board({
      models: [
        { rank: 1, name: 'ModelA', value: 1000, users: 5, requests: 10, tokens: 1000 },
        { rank: 2, name: 'ModelB', value: 1000, users: 2, requests: 10, tokens: 1000 },
      ],
    });
    vi.spyOn(api, 'get').mockResolvedValue(tiedBoard);
    await mountPanel();

    const modelRows = panelHost.querySelectorAll('.oa-board-list')[1]!.querySelectorAll('.oa-board-row');
    expect(modelRows[0]!.querySelector('.oa-board-rank')?.textContent).toBe('1');
    expect(modelRows[1]!.querySelector('.oa-board-rank')?.textContent).toBe('2');
  });

  it('names nobody the server did not name, including itself', async () => {
    vi.spyOn(api, 'get').mockResolvedValue(board({
      identity: 'anonymous',
      accounts: [
        { rank: 1, value: 5000, requests: 40, tokens: 5000, models: 2 },
        { rank: 2, name: 'Member', value: 1200, requests: 9, tokens: 1200, models: 1, self: true },
      ],
    }));
    await mountPanel();

    const rows = panelHost.querySelectorAll('.oa-board-row');
    expect(rows[0]!.textContent).toContain(t('boardAnonymous', { rank: 1 }));
    expect(rows[0]!.textContent).not.toContain('Otter');
    // The circle where a picture would go carries the place number, not a
    // name's initial: there is no name, and `initials` on the place label
    // returns word salad ("第 1 名" → "第名").
    expect(rows[0]!.querySelector('.oa-board-avatar')?.textContent?.trim()).toBe('1');
    expect(rows[1]!.querySelector('.oa-board-avatar')?.textContent?.trim()).toBe('M');
    // The reader is still told which row is theirs.
    expect(rows[1]!.textContent).toContain('Member');
  });

  it('says so rather than showing an empty board to somebody who has not used it', async () => {
    vi.spyOn(api, 'get').mockResolvedValue(board({
      accounts: [],
      me: { rank: 0, value: 0, gap: 0, participants: 0 },
      models: [],
    }));
    await mountPanel();

    const text = panelHost.textContent ?? '';
    expect(text).toContain(t('boardNotRanked'));
    expect(text).toContain(t('nothingYet'));
    expect(text).not.toContain(t('boardYourRank', { total: 0 }));
  });
});

describe('the leaderboard entry in the account menu', () => {
  async function openMenu(account: Account = ACCOUNT): Promise<string> {
    app = createApp({ render: () => h(AccountMenu, { account }) });
    app.use(createRouter({
      history: createMemoryHistory(),
      routes: [{ path: '/:p(.*)*', component: { render: () => null } }],
    }));
    app.mount(host);
    await nextTick();
    host.querySelector<HTMLButtonElement>('.oa-account-btn')!.click();
    await nextTick();
    return document.body.textContent ?? '';
  }

  it('is absent until the operator publishes it', async () => {
    adopt(ACCOUNT);
    const text = await openMenu();
    // The menu itself drew — it is the leaderboard that is missing.
    expect(text).toContain(t('apiKeys'));
    expect(text).not.toContain(t('leaderboardTitle'));
  });

  it('appears once it is published', async () => {
    adopt(ACCOUNT);
    site.value = { leaderboard_show_users: true } as never;
    expect(await openMenu()).toContain(t('leaderboardTitle'));
  });

  it('is offered to its curator before it is published', async () => {
    adopt({ ...ACCOUNT, role: 'admin', admin_permissions: ['leaderboard'] });
    expect(await openMenu({ ...ACCOUNT, role: 'admin', admin_permissions: ['leaderboard'] }))
      .toContain(t('leaderboardTitle'));
  });
});
