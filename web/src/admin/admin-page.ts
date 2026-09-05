// The administration shell: the same header the chat has, a rail of sections,
// and whichever page the route names.
//
// The rail is the conversation rail with different contents — same width,
// same radius, same surface, same hover tint — so moving between chatting and
// administering does not feel like moving between two applications.

import { health } from '../api/client';
import { renderShell } from '../app/shell';
import { t, type StringKey } from '../i18n';
import { navigate } from '../router';
import { ICONS, button, clear, el, icon } from '../ui/dom';
import { attachResizer } from '../ui/resizer';
import { renderAnnouncements } from './announcements';
import { renderDashboard } from './dashboard';
import { renderGroups } from './groups';
import { renderModels } from './models';
import { renderProviders } from './providers';
import { renderSettings } from './settings';
import { renderUsage } from './usage';
import { renderUsers } from './users';

export interface AdminPage {
  /** The path segment after /admin, empty for the dashboard. */
  slug: string;
  label: StringKey;
  icon: readonly string[];
  render(view: AdminView): void | Promise<void>;
}

/** What a page draws into, plus the handles it needs to do its job. */
export interface AdminView {
  /** The scrolling content area. */
  body: HTMLElement;
  /** The flex row the side panel becomes a column of. */
  host: HTMLElement;
  /** Left of the title. */
  setTitle(title: string, subtitle?: string): void;
  /** Right of the title — "Add a provider", a filter, a refresh. */
  actions: HTMLElement;
  /** Re-runs the current page, after a save. */
  reload(): void;
  /** Everything after /admin/<slug>/, for a nested selection. */
  params: string[];
}

const PAGES: AdminPage[] = [
  { slug: '', label: 'navDashboard', icon: ICONS.home, render: renderDashboard },
  { slug: 'users', label: 'navUsers', icon: ICONS.users, render: renderUsers },
  { slug: 'groups', label: 'navGroups', icon: ICONS.layers, render: renderGroups },
  { slug: 'providers', label: 'navProviders', icon: ICONS.server, render: renderProviders },
  { slug: 'models', label: 'navModels', icon: ICONS.spark, render: renderModels },
  { slug: 'usage', label: 'navUsage', icon: ICONS.chart, render: renderUsage },
  { slug: 'settings', label: 'navSettings', icon: ICONS.sliders, render: renderSettings },
  { slug: 'announcements', label: 'announcements', icon: ICONS.file, render: renderAnnouncements },
];

export function renderAdminPage(root: HTMLElement, path: string): void {
  const segments = path.replace(/^\/admin\/?/, '').split('/').filter(Boolean);
  const slug = segments[0] ?? '';
  const page = PAGES.find((entry) => entry.slug === slug) ?? PAGES[0]!;

  const shell = renderShell(root);
  shell.body.classList.add('oa-admin');

  const rail = el('div', 'oa-admin-rail');
  rail.appendChild(el('span', 'oa-admin-rail-title', t('administration')));
  for (const entry of PAGES) {
    const item = el('a', `oa-admin-nav${entry === page ? ' active' : ''}`);
    item.href = entry.slug ? `/admin/${entry.slug}` : '/admin';
    item.appendChild(icon(entry.icon, 15));
    item.appendChild(el('span', null, t(entry.label)));
    rail.appendChild(item);
  }

  const foot = el('div', 'oa-admin-rail-foot');
  foot.appendChild(button('oa-btn', t('backToChat'), () => navigate('/')));
  // Which build is running, from the server rather than from the bundle: the
  // two can differ behind a stale cache, and the server's answer is the one
  // that matters.
  const build = el('span', 'oa-admin-build', '');
  foot.appendChild(build);
  rail.appendChild(foot);

  void health()
    .then((status) => {
      build.textContent = status.version;
      build.title = `Build ${status.version} · up ${formatUptime(status.uptime_sec)}`;
    })
    .catch(() => {
      // A version nobody can read is not worth an error state.
    });

  const main = el('div', 'oa-admin-main');
  const head = el('div', 'oa-admin-head');
  const heading = el('div');
  const title = el('h1', 'oa-admin-title', t(page.label));
  const subtitle = el('p', 'oa-admin-subtitle');
  subtitle.hidden = true;
  heading.appendChild(title);
  heading.appendChild(subtitle);

  const actions = el('div', 'oa-admin-actions');
  head.appendChild(heading);
  head.appendChild(el('span', 'oa-admin-head-spacer'));
  head.appendChild(actions);

  const body = el('div', 'oa-admin-body');
  main.appendChild(head);
  main.appendChild(body);

  shell.body.appendChild(rail);
  shell.body.appendChild(main);

  attachResizer({
    target: rail,
    edge: 'right',
    cssVariable: '--oa-admin-rail-width',
    styleTarget: rail,
    storageKey: 'obsidian-arc-admin-rail-width',
    min: 170,
    max: 380,
    fallback: 220,
    label: t('resizeNav'),
  });

  const view: AdminView = {
    body,
    host: shell.body,
    actions,
    params: segments.slice(1),
    setTitle(next, hint) {
      title.textContent = next;
      subtitle.textContent = hint ?? '';
      subtitle.hidden = !hint;
    },
    reload() {
      clear(body);
      clear(actions);
      body.appendChild(loading());
      void page.render(view);
    },
  };

  body.appendChild(loading());
  void page.render(view);
}

export function formatUptime(seconds: number): string {
  if (seconds < 60) return `${seconds}s`;
  if (seconds < 3600) return `${Math.round(seconds / 60)}m`;
  if (seconds < 86400) return `${Math.round(seconds / 3600)}h`;
  return `${Math.round(seconds / 86400)}d`;
}

export function loading(): HTMLElement {
  const wrap = el('div', 'oa-table-empty', t('loading'));
  return wrap;
}

/** Renders a failed load in place, with a way to try again. */
export function failure(view: AdminView, error: unknown): void {
  clear(view.body);
  const wrap = el('div', 'oa-notice');
  wrap.appendChild(el('h2', 'oa-notice-title', t('couldNotLoad')));
  wrap.appendChild(el('p', 'oa-notice-body', error instanceof Error ? error.message : String(error)));
  wrap.appendChild(button('oa-btn', t('tryAgain'), () => view.reload()));
  view.body.appendChild(wrap);
}
