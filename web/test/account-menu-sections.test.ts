// The account menu is read in sections now: account, tools, community and
// support, with administration and sign-out at the foot. Moving rows between
// sections must not move them past the conditions that decide whether they
// are drawn at all, so every condition the flat list had is held here
// against the grouped one.

import fs from 'node:fs';
import path from 'node:path';
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import { createApp, h, nextTick, ref, type App } from 'vue';
import { createMemoryHistory, createRouter } from 'vue-router';
import type { Account } from '../src/api/auth';
import AccountMenu from '../src/layouts/AccountMenu.vue';
import { changeLanguage, t } from '../src/composables/useI18n';
import { installPlugins } from '../src/plugins/registry';
import { adopt, forget, site, siteInfo } from '../src/stores/session';
import { makeAccount } from './fixtures/account';

vi.mock('../src/stores/feedback', () => ({
  feedbackUnread: ref(0),
  refreshFeedbackUnread: vi.fn(async () => {}),
  forgetFeedbackUnread: vi.fn(),
}));

const member = makeAccount({ username: 'ada', nickname: 'Ada', group_name: 'Default' });

let app: App | undefined;

async function openMenu(account: Account): Promise<HTMLElement> {
  const host = document.createElement('div');
  document.body.appendChild(host);
  app = createApp({ render: () => h(AccountMenu, { account }) });
  app.use(createRouter({ history: createMemoryHistory(), routes: [{ path: '/:p(.*)*', component: { render: () => null } }] }));
  app.mount(host);
  await nextTick();
  host.querySelector<HTMLButtonElement>('.oa-account-btn')!.click();
  await vi.waitFor(() => expect(host.querySelector('.oa-acct-head')).not.toBeNull());
  await nextTick();
  return document.querySelector<HTMLElement>('.oa-menu-account')!;
}

/** Each section's label, and the titles of the rows under it, in order. */
function sections(menu: HTMLElement): Array<[string, string[]]> {
  return [...menu.querySelectorAll('.oa-acct-section')].map((section) => [
    section.querySelector('.oa-acct-label')?.textContent?.trim() ?? '',
    [...section.querySelectorAll('.oa-menu-item-title')].map((node) => node.textContent?.trim() ?? ''),
  ]);
}

function foot(menu: HTMLElement): string[] {
  return [...menu.querySelectorAll('.oa-acct-foot .oa-menu-item-title')].map((node) => node.textContent?.trim() ?? '');
}

beforeEach(async () => { await changeLanguage('en'); });
afterEach(() => {
  app?.unmount();
  app = undefined;
  document.body.textContent = '';
  site.value = null;
  forget();
  installPlugins([]);
});

describe('the account menu sections', () => {
  it('groups every entry under its section, with the way out at the foot', async () => {
    adopt({ ...member, role: 'super_admin' });
    site.value = { ...siteInfo.value, health_show_users: true, leaderboard_show_users: true };
    const menu = await openMenu({ ...member, role: 'super_admin', allow_terminal: true });

    expect(sections(menu)).toEqual([
      [t('account'), [t('settings'), t('navUsage'), t('archivedConversations')]],
      [t('acctMenuTools'), [t('imageLab'), t('apiKeys'), t('navTerminal')]],
      [t('acctMenuCommunity'), [t('uptimeTitle'), t('leaderboardTitle')]],
      [t('acctMenuSupport'), [t('feedback'), t('about')]],
    ]);
    expect(foot(menu)).toEqual([t('administration'), t('signOut')]);
  });

  it('keeps the account itself in the head, with the group and role as text', async () => {
    adopt({ ...member, role: 'super_admin' });
    const menu = await openMenu({ ...member, role: 'super_admin' });

    expect(menu.querySelector('.oa-acct-name')?.textContent).toBe('Ada');
    expect(menu.querySelector('.oa-acct-handle')?.textContent).toBe('@ada');
    expect([...menu.querySelectorAll('.oa-acct-fact')].map((node) => node.textContent))
      .toEqual(['Default', t('superAdmin')]);
    // No chips: the group was an uppercase pill, which is what this replaced.
    expect(menu.querySelector('.oa-badge')).toBeNull();
  });

  it('offers administration to an administrator alone', async () => {
    adopt(member);
    let menu = await openMenu(member);
    expect(foot(menu)).toEqual([t('signOut')]);
    expect(menu.querySelector('.oa-acct-fact:nth-child(2)')).toBeNull();

    app?.unmount();
    document.body.textContent = '';
    adopt({ ...member, role: 'admin' });
    menu = await openMenu({ ...member, role: 'admin' });
    expect(foot(menu)).toEqual([t('administration'), t('signOut')]);
    expect([...menu.querySelectorAll('.oa-acct-fact')].map((node) => node.textContent)).toContain(t('admin'));
  });

  it('drops the terminal from the tools when the group has it switched off', async () => {
    adopt(member);
    const menu = await openMenu({ ...member, allow_terminal: false });
    const tools = sections(menu).find(([label]) => label === t('acctMenuTools'))!;

    expect(tools[1]).toEqual([t('imageLab'), t('apiKeys')]);
  });

  it('drops the community section, label and all, when neither entry is published', async () => {
    adopt(member);
    const menu = await openMenu(member);
    const labels = sections(menu).map(([label]) => label);

    expect(labels).toEqual([t('account'), t('acctMenuTools'), t('acctMenuSupport')]);
    expect(menu.textContent).not.toContain(t('acctMenuCommunity'));
  });

  it.each([
    ['health_show_users', 'uptimeTitle'],
    ['leaderboard_show_users', 'leaderboardTitle'],
  ] as const)('shows the community section with %s alone', async (flag, titleKey) => {
    adopt(member);
    site.value = { ...siteInfo.value, [flag]: true };
    const menu = await openMenu(member);
    const community = sections(menu).find(([label]) => label === t('acctMenuCommunity'));

    expect(community?.[1]).toEqual([t(titleKey)]);
  });

  it("puts a plugin's panels in the sections they ask for", async () => {
    installPlugins([{
      name: 'demo',
      userPanels: [
        { slug: 'later', title: () => 'Later panel' },
        { slug: 'upgrade', title: () => 'Upgrade panel', menu: 'usage' },
      ],
    }]);
    adopt(member);
    const menu = await openMenu(member);
    const byLabel = new Map(sections(menu));

    expect(byLabel.get(t('account'))).toEqual([t('settings'), t('navUsage'), 'Upgrade panel', t('archivedConversations')]);
    expect(byLabel.get(t('acctMenuSupport'))).toEqual([t('feedback'), 'Later panel', t('about')]);
  });

  it('labels each section for a screen reader as well as on screen', async () => {
    adopt(member);
    const menu = await openMenu(member);

    for (const section of menu.querySelectorAll('.oa-acct-section')) {
      expect(section.getAttribute('role')).toBe('group');
      expect(section.getAttribute('aria-label')).toBe(section.querySelector('.oa-acct-label')?.textContent?.trim());
    }
  });
});

// The header's utilities share one pill. Two cascade details jsdom cannot
// see hold it together, read from the source so they hold without a build.
describe('the header tools pill in the stylesheet source', () => {
  const workspace = fs.readFileSync(path.resolve(__dirname, '../src/styles/_workspace.scss'), 'utf8')
    .replace(/\/\*[\s\S]*?\*\//g, '');

  it('clears the per-button wallpaper fill before the shared hover rule, so hover still lights a button', () => {
    const clear = workspace.indexOf('.has-wallpaper .oa-header-tools .oa-icon-btn {');
    const hover = workspace.indexOf('.oa-icon-btn:hover:not(:disabled) {');

    expect(clear).toBeGreaterThan(-1);
    expect(hover).toBeGreaterThan(clear);
  });

  it('frosts the pill on a pseudo-element, so the bell menu inside it is not trapped in a stacking context', () => {
    const pill = /\.has-wallpaper \.oa-header-tools \{([^}]*)\}/.exec(workspace)?.[1] ?? '';
    const layer = /\.has-wallpaper \.oa-header-tools::before \{([^}]*)\}/.exec(workspace)?.[1] ?? '';

    expect(pill).not.toMatch(/backdrop-filter/);
    expect(layer).toMatch(/backdrop-filter/);
  });
});
