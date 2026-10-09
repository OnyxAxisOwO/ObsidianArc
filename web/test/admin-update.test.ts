import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import { createApp, h, nextTick, shallowRef, type App } from 'vue';
import { createMemoryHistory, createRouter } from 'vue-router';
import { adminApi, type UpdateStatus } from '../src/admin/api';
import { hasCloudflareNotice, loadUpdateStatus, offeredRelease, resetUpdateStatus } from '../src/admin/update';
import type { Account } from '../src/api/auth';
import { changeLanguage } from '../src/composables/useI18n';
import { providePanelHost } from '../src/composables/usePanelHost';
import { adopt } from '../src/stores/session';
import AdminDashboard from '../src/views/admin/AdminDashboard.vue';
import AdminUpdateDialog from '../src/views/admin/AdminUpdateDialog.vue';
import { provideAdminView } from '../src/views/admin/adminView';
import { dashboardFixture } from './fixtures/dashboard';

const SKIPPED_KEY = 'obsidian-arc-admin-update-skipped';

let app: App | undefined;
let host: HTMLElement;
let actions: HTMLElement;

async function settle(): Promise<void> {
  await new Promise((resolve) => setTimeout(resolve, 0));
  await nextTick();
}

function status(overrides: Partial<UpdateStatus> = {}): UpdateStatus {
  return {
    current: 'v0.9.2',
    latest: 'v0.10.0',
    update_available: true,
    name: 'Obsidian Arc v0.10.0',
    notes: '## Changes\n\n- Faster replies',
    url: 'https://github.com/OnyxAxisOwO/ObsidianArc/releases/tag/v0.10.0',
    published_at: '2026-10-01T12:00:00Z',
    check_disabled: false,
    notices: [],
    ...overrides,
  };
}

function account(role: Account['role'], permissions: string[] = []): Account {
  return {
    id: 'operator', username: 'operator', nickname: 'Operator', email: '', bio: '', avatar: '',
    role, status: 'active', group_id: '', group_expires_at: 0, admin_permissions: permissions,
    created_at: Date.now(), updated_at: Date.now(), last_login_at: 0, group_name: '',
    email_verified: true, allow_stats: true, allow_delete_conversations: true,
    api_restricted: false, api_restricted_until: 0, api_restriction_source: '',
  };
}

async function mountDialog(): Promise<HTMLElement> {
  const panelHost = document.createElement('div');
  document.body.append(panelHost);
  app = createApp({ setup() {
    providePanelHost(shallowRef(panelHost));
    return () => h(AdminUpdateDialog);
  } });
  app.mount(host);
  await settle();
  return panelHost;
}

async function mountDashboard(): Promise<void> {
  const router = createRouter({ history: createMemoryHistory(), routes: [{ path: '/:pathMatch(.*)*', component: { render: () => null } }] });
  app = createApp({ setup() {
    provideAdminView({ actionsHost: actions, setTitle() {}, reload() {}, params: [] });
    return () => h(AdminDashboard);
  } });
  app.use(router);
  app.mount(host);
  await settle();
}

beforeEach(async () => {
  await changeLanguage('en');
  localStorage.clear();
  resetUpdateStatus();
  host = document.createElement('div');
  actions = document.createElement('div');
  document.body.append(host, actions);
  vi.spyOn(adminApi, 'dashboard').mockResolvedValue(dashboardFixture());
});

afterEach(() => {
  app?.unmount();
  app = undefined;
  document.body.textContent = '';
  localStorage.clear();
  vi.restoreAllMocks();
});

describe('the release dialog', () => {
  it('offers a newer release with its notes rendered and its page linked', async () => {
    vi.spyOn(adminApi, 'update').mockResolvedValue(status());
    await loadUpdateStatus();
    const panelHost = await mountDialog();

    const panel = panelHost.querySelector('.oa-panel');
    expect(panel?.querySelector('.oa-panel-title')?.textContent).toBe('Obsidian Arc v0.10.0 is available');
    expect(panel?.querySelector('.oa-update-meta')?.textContent).toContain('You are running v0.9.2.');
    expect(panel?.querySelector('.oa-update-notes h2')?.textContent).toBe('Changes');
    const link = panel?.querySelector<HTMLAnchorElement>('a.oa-btn');
    expect(link?.getAttribute('href')).toBe('https://github.com/OnyxAxisOwO/ObsidianArc/releases/tag/v0.10.0');
    expect(link?.getAttribute('target')).toBe('_blank');
    expect(link?.getAttribute('rel')).toContain('noopener');
  });

  it('stays closed for a version the administrator skipped, and remembers that', async () => {
    vi.spyOn(adminApi, 'update').mockResolvedValue(status());
    await loadUpdateStatus();
    const panelHost = await mountDialog();

    const skip = [...panelHost.querySelectorAll<HTMLButtonElement>('.oa-panel button')]
      .find((node) => node.textContent?.trim() === 'Skip this version');
    skip?.click();
    await nextTick();
    expect(panelHost.querySelector('.oa-panel')).toBeNull();
    expect(localStorage.getItem(SKIPPED_KEY)).toBe('v0.10.0');
  });

  it('does not reopen for a skipped version on the next visit', async () => {
    localStorage.setItem(SKIPPED_KEY, 'v0.10.0');
    vi.spyOn(adminApi, 'update').mockResolvedValue(status());
    await loadUpdateStatus();
    const panelHost = await mountDialog();

    expect(panelHost.querySelector('.oa-panel')).toBeNull();
  });

  it('speaks again once a release after the skipped one is out', async () => {
    localStorage.setItem(SKIPPED_KEY, 'v0.10.0');
    vi.spyOn(adminApi, 'update').mockResolvedValue(status({ latest: 'v0.11.0' }));
    await loadUpdateStatus();
    const panelHost = await mountDialog();

    expect(panelHost.querySelector('.oa-panel-title')?.textContent).toContain('v0.11.0');
  });

  it('says nothing when no newer release exists', async () => {
    vi.spyOn(adminApi, 'update').mockResolvedValue(status({ update_available: false, latest: 'v0.9.2' }));
    await loadUpdateStatus();
    const panelHost = await mountDialog();

    expect(panelHost.querySelector('.oa-panel')).toBeNull();
  });

  it('asks the server once per page load, however many screens want the answer', async () => {
    const update = vi.spyOn(adminApi, 'update').mockResolvedValue(status());
    await Promise.all([loadUpdateStatus(), loadUpdateStatus(), loadUpdateStatus()]);
    await loadUpdateStatus();

    expect(update).toHaveBeenCalledTimes(1);
  });

  it('asks again after a failed answer, rather than showing nothing for the page', async () => {
    const update = vi.spyOn(adminApi, 'update')
      .mockRejectedValueOnce(new Error('locked'))
      .mockResolvedValueOnce(status());
    await loadUpdateStatus();
    await loadUpdateStatus();

    expect(update).toHaveBeenCalledTimes(2);
  });
});

describe('the offered release', () => {
  it('is withheld when the version is skipped and offered otherwise', () => {
    const current = status();
    expect(offeredRelease(current, null)?.latest).toBe('v0.10.0');
    expect(offeredRelease(current, 'v0.10.0')).toBeNull();
    expect(offeredRelease(current, 'v0.9.0')?.latest).toBe('v0.10.0');
    expect(offeredRelease(null, null)).toBeNull();
  });

  it('recognises the unclaimed Cloudflare notice and nothing else', () => {
    expect(hasCloudflareNotice(status({ notices: [{ kind: 'cloudflare_unclaimed' }] }))).toBe(true);
    expect(hasCloudflareNotice(status({ notices: [] }))).toBe(false);
    expect(hasCloudflareNotice(null)).toBe(false);
  });
});

describe('the dashboard notices', () => {
  it('shows the update and the Cloudflare warning to a super administrator', async () => {
    adopt(account('super_admin'));
    vi.spyOn(adminApi, 'update').mockResolvedValue(status({ notices: [{ kind: 'cloudflare_unclaimed' }] }));
    await mountDashboard();

    const update = host.querySelector('#secUpdateNotice');
    expect(update?.textContent).toContain('v0.10.0');
    expect(update?.querySelector('a')?.getAttribute('href')).toBe('https://github.com/OnyxAxisOwO/ObsidianArc/releases/tag/v0.10.0');

    const cloudflare = host.querySelector('#secCloudflareNotice');
    expect(cloudflare?.classList.contains('is-warning')).toBe(true);
    expect(cloudflare?.textContent).toContain('OBSIDIAN_TRUST_CLOUDFLARE=true');
  });

  it('keeps the update line even after its dialog was skipped', async () => {
    adopt(account('super_admin'));
    localStorage.setItem(SKIPPED_KEY, 'v0.10.0');
    vi.spyOn(adminApi, 'update').mockResolvedValue(status());
    await mountDashboard();

    expect(host.querySelector('#secUpdateNotice')).not.toBeNull();
    expect(host.querySelector('#secCloudflareNotice')).toBeNull();
  });

  it('asks nothing of, and shows nothing to, a delegated administrator', async () => {
    adopt(account('admin', ['settings']));
    const update = vi.spyOn(adminApi, 'update').mockResolvedValue(status({ notices: [{ kind: 'cloudflare_unclaimed' }] }));
    await mountDashboard();

    expect(update).not.toHaveBeenCalled();
    expect(host.querySelector('#secUpdateNotice')).toBeNull();
    expect(host.querySelector('#secCloudflareNotice')).toBeNull();
  });
});
