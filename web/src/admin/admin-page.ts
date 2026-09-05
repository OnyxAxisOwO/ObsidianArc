// The administration shell: the same header the chat has, a rail of sections,
// and whichever page the route names.
//
// The rail is the conversation rail with different contents — same width,
// same radius, same surface, same hover tint — so moving between chatting and
// administering does not feel like moving between two applications.

import { renderShell } from '../app/shell';
import { navigate } from '../router';
import { ICONS, button, clear, el, icon } from '../ui/dom';
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
  label: string;
  icon: readonly string[];
  render(view: AdminView): void | Promise<void>;
}

/** What a page draws into, plus the handles it needs to do its job. */
export interface AdminView {
  /** The scrolling content area. */
  body: HTMLElement;
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
  { slug: '', label: 'Dashboard', icon: ICONS.home, render: renderDashboard },
  { slug: 'users', label: 'Users', icon: ICONS.users, render: renderUsers },
  { slug: 'groups', label: 'Groups', icon: ICONS.layers, render: renderGroups },
  { slug: 'providers', label: 'Providers', icon: ICONS.server, render: renderProviders },
  { slug: 'models', label: 'Models', icon: ICONS.spark, render: renderModels },
  { slug: 'usage', label: 'Usage', icon: ICONS.chart, render: renderUsage },
  { slug: 'settings', label: 'Settings', icon: ICONS.sliders, render: renderSettings },
];

export function renderAdminPage(root: HTMLElement, path: string): void {
  const segments = path.replace(/^\/admin\/?/, '').split('/').filter(Boolean);
  const slug = segments[0] ?? '';
  const page = PAGES.find((entry) => entry.slug === slug) ?? PAGES[0]!;

  const shell = renderShell(root);
  shell.body.classList.add('oa-admin');

  const rail = el('div', 'oa-admin-rail');
  rail.appendChild(el('span', 'oa-admin-rail-title', 'Administration'));
  for (const entry of PAGES) {
    const item = el('a', `oa-admin-nav${entry === page ? ' active' : ''}`);
    item.href = entry.slug ? `/admin/${entry.slug}` : '/admin';
    item.appendChild(icon(entry.icon, 15));
    item.appendChild(el('span', null, entry.label));
    rail.appendChild(item);
  }

  const foot = el('div', 'oa-admin-rail-foot');
  foot.appendChild(button('oa-btn', 'Back to chat', () => navigate('/')));
  rail.appendChild(foot);

  const main = el('div', 'oa-admin-main');
  const head = el('div', 'oa-admin-head');
  const heading = el('div');
  const title = el('h1', 'oa-admin-title', page.label);
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

  const view: AdminView = {
    body,
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

export function loading(): HTMLElement {
  const wrap = el('div', 'oa-table-empty', 'Loading…');
  return wrap;
}

/** Renders a failed load in place, with a way to try again. */
export function failure(view: AdminView, error: unknown): void {
  clear(view.body);
  const wrap = el('div', 'oa-notice');
  wrap.appendChild(el('h2', 'oa-notice-title', 'Could not load'));
  wrap.appendChild(el('p', 'oa-notice-body', error instanceof Error ? error.message : String(error)));
  wrap.appendChild(button('oa-btn', 'Try again', () => view.reload()));
  view.body.appendChild(wrap);
}
