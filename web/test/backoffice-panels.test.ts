import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import { createApp, nextTick, type App as VueApp } from 'vue';
import App from '../src/App.vue';
import { router } from '../src/router';
import { adopt, forget, site, siteInfo } from '../src/stores/session';
import type { Account } from '../src/api/auth';
import { changeLanguage, t } from '../src/composables/useI18n';
import { installPlugins } from '../src/plugins/registry';
import type { ArcPlugin } from '../src/plugins/types';

// A panel from the account menu, opened while the backoffice is on screen.
//
// The page must be the same DOM before and after, because a remounted page
// reads as the chat flashing up behind the column. The address must name the
// panel while keeping the page's own path, query and hash, and closing must
// give back exactly that address. The chat's own behaviour is checked after
// them, since it is the same menu.

const ACCOUNT: Account = {
  id: '01ARZ3NDEKTSV4RRFFQ69G5FAV',
  username: 'ada',
  email: 'ada@example.com',
  nickname: 'Ada',
  avatar: '',
  bio: '',
  role: 'user',
  group_id: 'g1',
  group_expires_at: 0,
  group_name: 'Default',
  status: 'active',
  created_at: Date.now(),
  updated_at: Date.now(),
  last_login_at: Date.now(),
  email_verified: true,
  allow_stats: true,
  allow_delete_conversations: true,
  api_restricted: false,
  api_restricted_until: 0,
  api_restriction_source: '',
  signup_user_agent: 'Mozilla/5.0 ObsidianArcTest/1.0',
};

const ADMIN: Account = { ...ACCOUNT, role: 'super_admin' };

/**
 * The answers the screens opened here reach for, in the shapes they expect: a
 * subset of the ones boot.test.ts gives, for the same screens.
 */
const BODIES: Array<[RegExp, unknown]> = [
  [/\/api\/admin\/providers/, { providers: [] }],
  [/\/api\/admin\/models/, { models: [] }],
  // The settings page cannot draw its workbench without these two answers.
  [/\/api\/admin\/settings/, { settings: {} }],
  [/\/api\/admin\/references/, { models: [] }],
  [/\/api\/admin\/groups/, { groups: [], policies: [] }],
  [/\/api\/admin\/meta/, { provider_kinds: ['openai', 'anthropic'], reasoning_styles: ['auto'] }],
  [/\/api\/admin\/logs\/facets/, {
    users: [], models: [], error_codes: [], statuses: [], total: 0, dropped: 0, oldest: 0,
  }],
  [/\/api\/admin\/logs/, { entries: [], total: 0, limit: 50, offset: 0 }],
  [/\/api\/health/, { status: 'ok', version: 'vtest', uptime_sec: 1 }],
  [/\/api\/profile\/two-factor$/, {
    available: true, enabled: false, enabled_at: 0, recovery_remaining: 0,
    mandatory: false, policy: 'optional', remember_days: 0,
  }],
  [/\/api\/announcements/, { announcements: [], unread: 0, popup: null }],
  [/\/api\/notifications\/poll/, { notifications: [], unread: 0 }],
  [/\/api\/notifications/, { notifications: [], unread: 0, seen_at: 0 }],
  [/\/api\/conversations/, { conversations: [] }],
  [/\/api\/keys/, { keys: [], enabled: false, max: 0 }],
  [/\/api\/models/, { models: [] }],
  [/\/api\/usage\/me$/, { unlimited: true, windows: [] }],
  [/\/api\/usage\/cards/, { cards: [] }],
  [/\/api\/uptime/, { uptime_sec: 10, models: [] }],
  [/\/api\/auth\/oauth\/connections/, {
    connections: [], has_password: true, providers: [{ id: 'github', name: 'GitHub', enabled: true }],
  }],
];

function stubServer(): void {
  vi.stubGlobal('fetch', vi.fn(async (input: RequestInfo | URL) => {
    const url = String(input);
    const match = BODIES.find(([pattern]) => pattern.test(url));
    return new Response(JSON.stringify(match?.[1] ?? {}), {
      status: 200,
      headers: { 'Content-Type': 'application/json' },
    });
  }));
}

let app: VueApp | null = null;
let host: HTMLElement;

function pause(ms: number): Promise<void> {
  return new Promise((resolve) => setTimeout(resolve, ms));
}

async function settle(): Promise<void> {
  await pause(0);
  await pause(0);
  await nextTick();
}

async function mountAt(path: string): Promise<void> {
  // A second address in one test is a second application, not a second route
  // of the first: the reload cases need the page built again from scratch.
  app?.unmount();
  host.replaceChildren();
  await router.replace(path);
  await router.isReady();
  app = createApp(App);
  app.use(router);
  app.mount(host);
  await settle();
  await new Promise((resolve) => requestAnimationFrame(resolve));
}

/** Opens the account menu and chooses the item with this title. */
async function chooseFromAccountMenu(title: string): Promise<void> {
  host.querySelector<HTMLButtonElement>('.oa-account-btn')!.click();
  await settle();
  const item = [...document.querySelectorAll<HTMLButtonElement>('.oa-menu-item')]
    .find((node) => node.querySelector('.oa-menu-item-title')?.textContent?.trim() === title);
  if (!item) throw new Error(`No account menu item: ${title}`);
  item.click();
  await settle();
}

function panelTitle(): string {
  return host.querySelector('.oa-panel .oa-panel-title')?.textContent?.trim() ?? '';
}

/** The close button, as the reader sees it; the panel slides out before it tells the page. */
async function closeWithButton(): Promise<void> {
  const button = host.querySelector<HTMLButtonElement>(`.oa-panel .oa-panel-head [aria-label="${t('close')}"]`);
  if (!button) throw new Error('no panel to close');
  button.click();
  await pause(400);
}

/** A plugin with one panel of entries, each opening a page of its own. */
function tipsPlugin(): ArcPlugin {
  return {
    name: 'tipper',
    userPanels: [{
      slug: 'tips',
      title: () => 'Tips',
      entries: {
        empty: () => 'Nothing yet',
        load: async () => [{ id: '42', title: 'Entry 42' }],
        open: async () => ({ title: 'Entry 42 in detail', controls: [] }),
      },
    }],
  } satisfies ArcPlugin;
}

/**
 * Whether the drawn panel is open. A panel that mounts slides in on the frame
 * after it mounts, so this waits for that frame before it looks.
 */
async function panelIsOpen(): Promise<boolean> {
  await new Promise((resolve) => requestAnimationFrame(resolve));
  await nextTick();
  return host.querySelector('.oa-panel')?.classList.contains('open') === true;
}

/** Opens the first entry the drawn plugin panel lists. */
async function openFirstEntry(): Promise<void> {
  host.querySelector<HTMLButtonElement>('.oa-feedback-item')!.click();
  await settle();
}

/** Where the first connect link returns the browser to, as the server will read it. */
function connectNext(): string | null {
  const href = host.querySelector<HTMLAnchorElement>('.oa-connection a')?.getAttribute('href') ?? '';
  return new URL(href, 'http://localhost').searchParams.get('next');
}

/** The messages the settings screen shows under its connections. */
function connectionFlashes(): string[] {
  return [...host.querySelectorAll('.oa-group-flash')].map((node) => node.textContent?.trim() ?? '');
}

/** Waits for what loads over the network, such as the list of connections. */
async function until(condition: () => boolean): Promise<void> {
  for (let attempt = 0; attempt < 50 && !condition(); attempt += 1) await pause(10);
}

/** A rail entry, workbench tab or subnav item, found by the label the reader sees. */
function controlNamed(selector: string, label: string): HTMLElement {
  const found = [...host.querySelectorAll<HTMLElement>(selector)]
    .find((node) => node.textContent?.trim() === label);
  if (!found) throw new Error(`No control named ${label}`);
  return found;
}

/** Types into a search field the way the reader does: the value, then its input event. */
async function search(selector: string, value: string): Promise<void> {
  const input = host.querySelector<HTMLInputElement>(selector)!;
  input.value = value;
  input.dispatchEvent(new Event('input', { bubbles: true }));
  await nextTick();
}

beforeEach(async () => {
  await changeLanguage('en');
  stubServer();
  // jsdom does not lay out, so it has no scrolling: a section is scrolled to,
  // and the page is scrolled back to its top, through these two.
  HTMLElement.prototype.scrollIntoView ??= () => {};
  Element.prototype.scrollTo ??= () => {};
  host = document.createElement('div');
  document.body.appendChild(host);
});

afterEach(() => {
  site.value = null;
  app?.unmount();
  app = null;
  host.remove();
  document.body.textContent = '';
  forget();
  installPlugins([]);
  vi.unstubAllGlobals();
});

describe('a panel from the account menu, opened over the backoffice', () => {
  it('opens beside the page, which stays mounted under the same address', async () => {
    adopt(ADMIN);
    await mountAt('/admin/providers?keep=1#models');
    const rail = host.querySelector('.oa-admin-rail');
    const main = host.querySelector('.oa-admin-main');
    expect(rail).not.toBeNull();

    await chooseFromAccountMenu(t('settings'));

    expect(router.currentRoute.value.path).toBe('/admin/providers');
    expect(router.currentRoute.value.query).toEqual({ keep: '1', panel: 'settings' });
    expect(router.currentRoute.value.hash).toBe('#models');
    // The same nodes, not equal ones: a remounted page would be new elements.
    expect(host.querySelector('.oa-admin-rail')).toBe(rail);
    expect(host.querySelector('.oa-admin-main')).toBe(main);
    // The panel is a column of the backoffice's row, not a page of the chat.
    const panel = host.querySelector('.oa-panel');
    expect(panel?.parentElement?.classList.contains('oa-chat-root')).toBe(true);
    expect(host.querySelector('.oa-settings-tabs')).not.toBeNull();
    expect(host.querySelector('.ai-chat-sidebar')).toBeNull();
  });

  it('closes back to the same path, query and hash, without remounting the page', async () => {
    adopt(ADMIN);
    await mountAt('/admin/providers?keep=1#models');
    const rail = host.querySelector('.oa-admin-rail');

    await chooseFromAccountMenu(t('settings'));
    await closeWithButton();

    expect(router.currentRoute.value.fullPath).toBe('/admin/providers?keep=1#models');
    expect(host.querySelector('.oa-panel')).toBeNull();
    expect(host.querySelector('.oa-admin-rail')).toBe(rail);
  });

  it('draws About the same way, and closes with Escape', async () => {
    adopt(ADMIN);
    await mountAt('/admin/providers?tab=manage');
    const rail = host.querySelector('.oa-admin-rail');

    await chooseFromAccountMenu(t('about'));
    expect(router.currentRoute.value.query).toEqual({ tab: 'manage', panel: 'about' });
    expect(host.querySelector('.oa-about-name')).not.toBeNull();
    expect(host.querySelector('.oa-admin-rail')).toBe(rail);

    document.dispatchEvent(new KeyboardEvent('keydown', { key: 'Escape' }));
    await pause(400);
    expect(router.currentRoute.value.fullPath).toBe('/admin/providers?tab=manage');
    expect(host.querySelector('.oa-panel')).toBeNull();
  });

  it('draws a plugin panel, opens its entry and steps back through the list', async () => {
    installPlugins([tipsPlugin()]);
    adopt(ADMIN);
    await mountAt('/admin/providers?tab=manage');
    const rail = host.querySelector('.oa-admin-rail');

    await chooseFromAccountMenu('Tips');
    expect(router.currentRoute.value.query).toEqual({ tab: 'manage', panel: 'x/tips' });
    expect(panelTitle()).toBe('Tips');

    host.querySelector<HTMLButtonElement>('.oa-feedback-item')!.click();
    await settle();
    expect(router.currentRoute.value.query).toEqual({ tab: 'manage', panel: 'x/tips/42' });
    expect(panelTitle()).toBe('Entry 42 in detail');

    host.querySelector<HTMLButtonElement>(`.oa-panel .oa-panel-head [aria-label="${t('back')}"]`)!.click();
    await pause(400);
    expect(router.currentRoute.value.query).toEqual({ tab: 'manage', panel: 'x/tips' });
    expect(panelTitle()).toBe('Tips');

    await closeWithButton();
    expect(router.currentRoute.value.fullPath).toBe('/admin/providers?tab=manage');
    expect(host.querySelector('.oa-admin-rail')).toBe(rail);
  });

  it('returns from an entry to the list with its X and with Escape, and the list is drawn', async () => {
    installPlugins([tipsPlugin()]);
    adopt(ADMIN);
    await mountAt('/admin/providers?tab=manage');
    await chooseFromAccountMenu('Tips');

    // An entry's X slides its own panel out, and the address then moves to the
    // list. The list must come back drawn, not as the hidden column it was.
    await openFirstEntry();
    await closeWithButton();
    expect(router.currentRoute.value.query).toEqual({ tab: 'manage', panel: 'x/tips' });
    expect(panelTitle()).toBe('Tips');
    expect(await panelIsOpen()).toBe(true);

    await openFirstEntry();
    document.dispatchEvent(new KeyboardEvent('keydown', { key: 'Escape' }));
    await pause(400);
    expect(router.currentRoute.value.query).toEqual({ tab: 'manage', panel: 'x/tips' });
    expect(panelTitle()).toBe('Tips');
    expect(await panelIsOpen()).toBe(true);

    // The list's own X still closes it, and the menu opens it again afterwards.
    await closeWithButton();
    expect(router.currentRoute.value.fullPath).toBe('/admin/providers?tab=manage');
    expect(host.querySelector('.oa-panel')).toBeNull();
    await chooseFromAccountMenu('Tips');
    expect(await panelIsOpen()).toBe(true);
  });

  it('is drawn from the address on a reload, and the Back button takes it away', async () => {
    adopt(ADMIN);
    await mountAt('/admin/providers?panel=about');
    expect(host.querySelector('.oa-about-name')).not.toBeNull();
    expect(host.querySelector('.oa-admin-rail')).not.toBeNull();

    await mountAt('/admin/providers?keep=1');
    await chooseFromAccountMenu(t('settings'));
    expect(host.querySelector('.oa-settings-tabs')).not.toBeNull();

    window.history.back();
    await pause(50);
    await settle();
    expect(router.currentRoute.value.fullPath).toBe('/admin/providers?keep=1');
    expect(host.querySelector('.oa-panel')).toBeNull();
    expect(host.querySelector('.oa-admin-rail')).not.toBeNull();
  });

  it('opens Settings on the tab its address names, not on the backoffice tab', async () => {
    adopt(ADMIN);
    await mountAt('/admin/providers?panel=settings/security');
    expect(host.querySelector('.oa-settings-tab.active')?.textContent?.trim()).toBe(t('secSecurity'));

    // The backoffice's own `tab` (plugin pages use it) is not Settings' tab.
    await mountAt('/admin/providers?tab=security');
    await chooseFromAccountMenu(t('settings'));
    expect(host.querySelector('.oa-settings-tab.active')?.textContent?.trim()).toBe(t('secAppearance'));
  });

  it('brings a connection back to the backoffice address the panel is drawn at', async () => {
    adopt(ADMIN);
    await mountAt('/admin/providers?keep=1#models');
    await chooseFromAccountMenu(t('settings'));
    await until(() => !!host.querySelector('.oa-connection a'));

    expect(connectNext()).toBe('/admin/providers?keep=1&panel=settings#models');
  });

  it('takes the outcome of a connection out of the address, and keeps the page as it was', async () => {
    adopt(ADMIN);
    await mountAt('/admin/providers?keep=1&panel=settings&oauth=connected#models');
    await settle();

    expect(router.currentRoute.value.fullPath).toBe('/admin/providers?keep=1&panel=settings#models');
    expect(connectionFlashes()).toContain(t('oauthConnected'));
    expect(host.querySelector('.oa-admin-rail')).not.toBeNull();
  });

  it('shows a connection that came back refused, in the panel it was started from', async () => {
    adopt(ADMIN);
    await mountAt('/admin/providers?panel=settings&oauth_error=denied');
    await settle();

    expect(router.currentRoute.value.fullPath).toBe('/admin/providers?panel=settings');
    expect(connectionFlashes()).toContain(t('oauthDenied'));
  });

  it('leaves the logs page filters alone, and does not fetch the log again', async () => {
    adopt(ADMIN);
    await mountAt('/admin/logs?outcome=failed');
    const logFetches = () => vi.mocked(fetch).mock.calls
      .filter(([url]) => /\/api\/admin\/logs(\?|$)/.test(String(url)))
      .length;
    const before = logFetches();

    await chooseFromAccountMenu(t('settings'));

    expect(router.currentRoute.value.query).toEqual({ outcome: 'failed', panel: 'settings' });
    expect(logFetches()).toBe(before);
  });

  it('applies the uptime and leaderboard rules to the panel as it applies them to the chat address', async () => {
    // Neither is granted to this administrator, and neither is published.
    adopt({ ...ACCOUNT, role: 'admin', admin_permissions: ['providers'] });
    site.value = { ...siteInfo.value, health_show_users: false, leaderboard_show_users: false };

    await mountAt('/admin/providers?panel=uptime');
    expect(router.currentRoute.value.fullPath).toBe('/admin/providers');
    expect(host.querySelector('.oa-panel')).toBeNull();

    await mountAt('/admin/providers?panel=leaderboard&keep=1');
    expect(router.currentRoute.value.fullPath).toBe('/admin/providers?keep=1');
    expect(host.querySelector('.oa-panel')).toBeNull();

    // Published, the same panel is drawn beside the page.
    site.value = { ...siteInfo.value, health_show_users: true };
    await mountAt('/admin/providers?panel=uptime');
    expect(router.currentRoute.value.fullPath).toBe('/admin/providers?panel=uptime');
    expect(panelTitle()).toBe(t('uptimeTitle'));
  });

  // Choosing the page on screen again is not a way out of the panel: the
  // address is the same page, so the panel's parameter comes with it.
  it('keeps the panel when the page on screen is chosen again in the rail', async () => {
    adopt(ADMIN);
    await mountAt('/admin/providers?panel=settings#models');
    const rail = host.querySelector('.oa-admin-rail');

    controlNamed('.oa-admin-rail .oa-admin-nav', t('navProviders')).click();
    await settle();

    expect(router.currentRoute.value.fullPath).toBe('/admin/providers?panel=settings');
    expect(host.querySelector('.oa-admin-rail')).toBe(rail);
    expect(await panelIsOpen()).toBe(true);
  });

  it('keeps the panel when the page on screen is chosen from a search result', async () => {
    adopt(ADMIN);
    await mountAt('/admin/providers?panel=settings#models');
    await search('.oa-admin-search input', 'providers');

    host.querySelector<HTMLAnchorElement>('.oa-admin-group-head')!.click();
    await settle();

    expect(router.currentRoute.value.fullPath).toBe('/admin/providers?panel=settings');
    expect(await panelIsOpen()).toBe(true);
  });

  it('keeps the panel when a section of the page on screen is chosen from a search result', async () => {
    adopt(ADMIN);
    await mountAt('/admin/settings?keep=1&panel=about');
    await search('.oa-admin-search input', t('controlAbout'));

    controlNamed('.oa-admin-subnav-item', t('controlAbout')).click();
    await settle();

    expect(router.currentRoute.value.fullPath).toBe('/admin/settings?keep=1&panel=about#secAbout');
    expect(await panelIsOpen()).toBe(true);
  });

  it('keeps the panel when a workbench tab is chosen while the page has a hash', async () => {
    adopt(ADMIN);
    await mountAt('/admin/settings?panel=about#somewhere');
    expect(host.querySelector('.oa-workbench')).not.toBeNull();

    controlNamed('.oa-workbench-tab', t('controlChat')).click();
    await settle();

    // The workbench drops its hash, as it always has; the address it leaves
    // is still the page's own, with the panel named in it.
    expect(router.currentRoute.value.fullPath).toBe('/admin/settings?panel=about');
    expect(await panelIsOpen()).toBe(true);
  });

  // A panel belongs to the page it was opened over. Choosing another page is
  // a new page, and the panel does not follow the reader there.
  it('closes the panel when another page of the backoffice is chosen', async () => {
    adopt(ADMIN);
    await mountAt('/admin/providers?panel=settings#models');

    controlNamed('.oa-admin-rail .oa-admin-nav', t('navGroups')).click();
    await settle();

    expect(router.currentRoute.value.fullPath).toBe('/admin/groups');
    expect(host.querySelector('.oa-panel')).toBeNull();
  });
});

describe('the same menu from the chat', () => {
  it('opens Settings at /settings, and closing it returns to the chat', async () => {
    adopt(ACCOUNT);
    await mountAt('/');

    await chooseFromAccountMenu(t('settings'));
    expect(router.currentRoute.value.fullPath).toBe('/settings');
    expect(host.querySelector('.ai-chat-sidebar')).not.toBeNull();
    expect(host.querySelector('.oa-settings-tabs')).not.toBeNull();

    await closeWithButton();
    expect(router.currentRoute.value.fullPath).toBe('/');
  });

  it('returns from an entry to the list with its X', async () => {
    installPlugins([tipsPlugin()]);
    adopt(ACCOUNT);
    await mountAt('/');

    await chooseFromAccountMenu('Tips');
    await openFirstEntry();
    expect(router.currentRoute.value.fullPath).toBe('/x/tips/42');

    await closeWithButton();
    expect(router.currentRoute.value.fullPath).toBe('/x/tips');
    expect(panelTitle()).toBe('Tips');
    expect(await panelIsOpen()).toBe(true);
  });

  it('brings a connection back to the settings screen, as it always has', async () => {
    adopt(ACCOUNT);
    await mountAt('/settings');
    await until(() => !!host.querySelector('.oa-connection a'));

    expect(connectNext()).toBe('/settings');
  });

  it('takes the outcome of a connection out of the chat address too', async () => {
    adopt(ACCOUNT);
    await mountAt('/settings?oauth=connected');
    await settle();

    expect(router.currentRoute.value.fullPath).toBe('/settings');
    expect(connectionFlashes()).toContain(t('oauthConnected'));
  });

  it('still reads the tab from the chat address', async () => {
    adopt(ACCOUNT);
    await mountAt('/settings?tab=security');
    expect(host.querySelector('.oa-settings-tab.active')?.textContent?.trim()).toBe(t('secSecurity'));
  });
});
