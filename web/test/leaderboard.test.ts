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

vi.mock('@/stores/feedback', () => ({
  feedbackUnread: ref(0),
  refreshFeedbackUnread: vi.fn(async () => {}),
  forgetFeedbackUnread: vi.fn(),
}));

const ACCOUNT: Account = {
  id: 'me', username: 'member', nickname: 'Member', email: '', qq: '', bio: '', avatar: '',
  role: 'user', status: 'active', group_id: 'g', group_expires_at: 0, group_name: 'Default',
  created_at: 0, updated_at: 0, last_login_at: 0, email_verified: true,
  allow_stats: true, allow_delete_conversations: true,
  api_restricted: false, api_restricted_until: 0, api_restriction_source: '',
};

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
  document.body.textContent = '';
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

    expect(api.get).toHaveBeenCalledWith('/api/leaderboard?period=week&metric=tokens');
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

  it('asks the server again when the window or the measure changes', async () => {
    const get = vi.spyOn(api, 'get').mockResolvedValue(board());
    await mountPanel();
    get.mockClear();

    const segments = panelHost.querySelectorAll('.oa-segment');
    segments[0]!.querySelectorAll('button')[2]!.click();
    await nextTick();
    await nextTick();
    expect(get).toHaveBeenCalledWith('/api/leaderboard?period=month&metric=tokens');

    segments[1]!.querySelectorAll('button')[1]!.click();
    await nextTick();
    await nextTick();
    expect(get).toHaveBeenLastCalledWith('/api/leaderboard?period=month&metric=requests');
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
