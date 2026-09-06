// The administration shell: the same header the chat has, a rail of sections,
// and whichever page the route names.
//
// The rail is the conversation rail with different contents — same width,
// same radius, same surface, same hover tint — so moving between chatting and
// administering does not feel like moving between two applications.

import { ApiError, health } from '../api/client';
import { showUnauthorizedModal } from '../auth/unauthorized-modal';
import { renderShell } from '../app/shell';
import { t, type StringKey } from '../i18n';
import { navigate } from '../router';
import { ICONS, button, clear, el, icon, iconButton } from '../ui/dom';
import { attachResizer } from '../ui/resizer';
import { closePanel } from '../ui/panel';
import { attachOverlayScrollbar, type OverlayScrollbarHandle } from '../ui/scrollbar';
import { formatUptime } from '../ui/table';
import { numberField, type Control } from '../ui/form';
import { adminApi, type AdminModel } from './api';
import { renderAnnouncements } from './announcements';
import { renderCodes } from './codes';
import { renderDashboard } from './dashboard';
import { renderGroups } from './groups';
import { renderModels } from './models';
import { renderProviders } from './providers';
import { renderSettings } from './settings';
import { renderLogs } from './logs';
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
  { slug: 'codes', label: 'navCodes', icon: ICONS.key, render: renderCodes },
  { slug: 'logs', label: 'navLogs', icon: ICONS.file, render: renderLogs },
  { slug: 'settings', label: 'navSettings', icon: ICONS.sliders, render: renderSettings },
  { slug: 'announcements', label: 'announcements', icon: ICONS.file, render: renderAnnouncements },
];

interface ActiveAdmin {
  root: HTMLElement;
  host: HTMLElement;
  main: HTMLElement;
  title: HTMLElement;
  subtitle: HTMLElement;
  actions: HTMLElement;
  bodyWrap: HTMLElement;
  body: HTMLElement;
  scrollbar: OverlayScrollbarHandle;
  navItems: Map<AdminPage, HTMLAnchorElement>;
  currentPage: AdminPage;
}

// Reused across /admin/* routes so clicking between tabs only animates the
// content area instead of destroying and recreating the rail and resizer.
let activeAdmin: ActiveAdmin | null = null;
let adminGeneration = 0;

export function renderAdminPage(root: HTMLElement, path: string): void {
  const segments = path.replace(/^\/admin\/?/, '').split('/').filter(Boolean);
  const slug = segments[0] ?? '';
  const page = PAGES.find((entry) => entry.slug === slug) ?? PAGES[0]!;

  if (activeAdmin && activeAdmin.root === root && activeAdmin.main.isConnected) {
    const fromIndex = PAGES.indexOf(activeAdmin.currentPage);
    const toIndex = PAGES.indexOf(page);
    activeAdmin.currentPage = page;

    for (const [entry, item] of activeAdmin.navItems) {
      item.classList.toggle('active', entry === page);
    }

    // Changing views should never leave a stale edit drawer open for a
    // record belonging to the previous section.
    closePanel(activeAdmin.host);

    activeAdmin.title.textContent = t(page.label);
    activeAdmin.subtitle.textContent = '';
    activeAdmin.subtitle.hidden = true;
    clear(activeAdmin.actions);

    const dirClass = toIndex > fromIndex ? 'enter-forward' : toIndex < fromIndex ? 'enter-back' : 'enter-rise';
    // Replacing the body node ensures any pending async renders from the
    // previous page target a detached subtree rather than leaking into this one.
    const body = el('div', `oa-admin-body ${dirClass}`);
    activeAdmin.bodyWrap.replaceChild(body, activeAdmin.body);
    activeAdmin.body = body;
    activeAdmin.scrollbar.setScrollElement(body);

    const currentGen = ++adminGeneration;
    const view: AdminView = {
      body,
      host: activeAdmin.host,
      actions: activeAdmin.actions,
      params: segments.slice(1),
      setTitle(next, hint) {
        if (currentGen !== adminGeneration) return;
        activeAdmin!.title.textContent = next;
        activeAdmin!.subtitle.textContent = hint ?? '';
        activeAdmin!.subtitle.hidden = !hint;
      },
      reload() {
        if (currentGen !== adminGeneration) return;
        clear(body);
        clear(activeAdmin!.actions);
        body.appendChild(loading());
        void page.render(view);
      },
    };

    body.appendChild(loading());
    void page.render(view);
    return;
  }

  const shell = renderShell(root);
  shell.body.classList.add('oa-admin');

  const navItems = new Map<AdminPage, HTMLAnchorElement>();
  const rail = el('div', 'oa-admin-rail');
  // The way out sits beside the heading rather than at the foot of the rail.
  // It is the one control here that leaves the backoffice, and the bottom of
  // a list of destinations is the last place anybody looks for it.
  const railHead = el('div', 'oa-admin-rail-head');
  railHead.appendChild(el('span', 'oa-admin-rail-title', t('administration')));
  railHead.appendChild(el('span', 'oa-header-spacer'));
  railHead.appendChild(iconButton('oa-icon-btn oa-admin-back', ICONS.chevron, t('backToChat'),
    () => navigate('/'), 16));
  rail.appendChild(railHead);
  for (const entry of PAGES) {
    const item = el('a', `oa-admin-nav${entry === page ? ' active' : ''}`);
    item.href = entry.slug ? `/admin/${entry.slug}` : '/admin';
    item.appendChild(icon(entry.icon, 15));
    item.appendChild(el('span', null, t(entry.label)));
    rail.appendChild(item);
    navItems.set(entry, item);
  }

  const foot = el('div', 'oa-admin-rail-foot');
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

  const bodyWrap = el('div', 'oa-admin-body-wrap');
  const body = el('div', 'oa-admin-body enter-rise');
  bodyWrap.appendChild(body);
  const scrollbar = attachOverlayScrollbar(body, bodyWrap);
  main.appendChild(head);
  main.appendChild(bodyWrap);

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

  activeAdmin = {
    root,
    host: shell.body,
    main,
    title,
    subtitle,
    actions,
    bodyWrap,
    body,
    scrollbar,
    navItems,
    currentPage: page,
  };

  const currentGen = ++adminGeneration;
  const view: AdminView = {
    body,
    host: shell.body,
    actions,
    params: segments.slice(1),
    setTitle(next, hint) {
      if (currentGen !== adminGeneration) return;
      title.textContent = next;
      subtitle.textContent = hint ?? '';
      subtitle.hidden = !hint;
    },
    reload() {
      if (currentGen !== adminGeneration) return;
      clear(body);
      clear(actions);
      body.appendChild(loading());
      void page.render(view);
    },
  };

  body.appendChild(loading());
  void page.render(view);
}

/**
 * One dropdown in an `.oa-filters` bar.
 *
 * Not `selectField` from ui/form.ts: that is a labelled row inside a form,
 * and this is a pill sitting in a row of pills above a table. Shared by the
 * users screen and the models screen so the two bars cannot drift apart, and
 * kept here rather than in ui/form.ts because that file is in the main
 * bundle and nothing outside the backoffice has a filter bar.
 */
export function filterSelect(
  options: Array<{ value: string; label: string }>,
  value: string,
): HTMLSelectElement {
  const select = el('select');
  for (const option of options) {
    const node = el('option', null, option.label);
    node.value = option.value;
    select.appendChild(node);
  }
  select.value = value;
  return select;
}

/**
 * The models an allowance is actually spent on. Fetched once per visit and
 * shared by every policy form, because three screens ask the same question
 * and none of them is worth a request of its own.
 */
let pricing: Promise<AdminModel[]> | null = null;

function pricedModels(): Promise<AdminModel[]> {
  if (!pricing) {
    pricing = adminApi.models()
      .then(({ models }) => models.filter((entry) => entry.enabled))
      // A failed lookup costs the note, not the form.
      .catch((): AdminModel[] => []);
  }
  return pricing;
}

/**
 * What one turn reserves before it runs, mirroring Model.WorstCase in Go.
 *
 * The reservation, not the average cost: it is what the limit is actually
 * compared against, so it is what decides whether an allowance can pay for
 * anything at all. A ceiling under one turn's reservation refuses the first
 * message rather than running out partway through the day, and that is worth
 * being told before saving rather than after a user complains.
 */
function worstCase(entry: AdminModel): number {
  const ceiling = entry.max_output_tokens > 0 ? entry.max_output_tokens : 4096;
  return entry.request_weight + (ceiling / 1000) * entry.output_token_weight;
}

/**
 * A credits limit with a live readout of what it buys.
 *
 * Credits are a unit this instance invents: how much one is worth is the
 * model weights, which are on another screen. A number with no idea attached
 * is the reason this setting was unreadable, so the field carries the
 * conversion instead of the operator having to do it.
 *
 * Priced against the most expensive model, because that is the one that
 * decides when somebody is cut off.
 */
export function creditsField(value: number | null): Control<number | null> {
  const note = el('span', 'oa-field-hint');
  let priciest: AdminModel | null = null;

  const paint = (): void => {
    const limit = control.value();
    if (!priciest || limit === null || limit <= 0) {
      note.textContent = '';
      return;
    }
    const cost = worstCase(priciest);
    const name = priciest.display_name;
    note.textContent = limit < cost
      ? t('creditsTooSmall', { name, cost: round(cost) })
      : t('creditsBuys', { turns: Math.floor(limit / cost), name, cost: round(cost) });
  };

  const control = numberField({
    label: t('limitCredits'),
    value,
    placeholder: t('noLimit'),
    min: 0,
    step: 0.1,
    onInput: paint,
  });
  control.element.appendChild(note);

  void pricedModels().then((models) => {
    priciest = models.reduce<AdminModel | null>(
      (worst, entry) => (worst === null || worstCase(entry) > worstCase(worst) ? entry : worst),
      null,
    );
    paint();
  });

  return control;
}

function round(value: number): string {
  return String(Math.round(value * 10) / 10);
}

export function loading(): HTMLElement {
  const wrap = el('div', 'oa-table-empty', t('loading'));
  return wrap;
}

/** Renders a failed load in place, with a way to try again. */
export function failure(view: AdminView, error: unknown): void {
  if (error instanceof ApiError && (error.status === 403 || error.code === 'forbidden')) {
    const root = activeAdmin?.root ?? document.getElementById('app');
    if (root) {
      showUnauthorizedModal(root);
      return;
    }
  }
  clear(view.body);
  const wrap = el('div', 'oa-notice');
  wrap.appendChild(el('h2', 'oa-notice-title', t('couldNotLoad')));
  wrap.appendChild(el('p', 'oa-notice-body', error instanceof Error ? error.message : String(error)));
  wrap.appendChild(button('oa-btn', t('tryAgain'), () => view.reload()));
  view.body.appendChild(wrap);
}
