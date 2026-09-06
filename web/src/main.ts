import './styles/base.css';
import './styles/chat.css';
import './styles/workspace.css';
import './styles/app.css';
import './styles/admin.css';

import { renderAboutPage } from './about/about-page';
import { renderAuthPage } from './auth/auth-page';
import { showUnauthorizedModal } from './auth/unauthorized-modal';
import { renderVerifyPage } from './auth/verify-page';
import { renderChatPage } from './chat/chat-page';
import { renderLandingPage } from './landing/landing-page';
import { renderKeysPage } from './settings/api-keys';
import { renderSettingsPage } from './settings/settings-page';
import { renderShell } from './app/shell';
import { loadLanguage, t } from './i18n';
import { navigate, startRouter, type Route, type RouteContext } from './router';
import { currentUser, isAdmin, siteInfo, start as startSession } from './session';
import { startTheme } from './theme/theme';
import { clear, el } from './ui/dom';

startTheme();

const root = document.getElementById('app');
if (!root) throw new Error('missing #app');

void boot();

async function boot(): Promise<void> {
  // The dictionary before the session: a slow or refused /api/auth/me must not
  // decide whether the sign-in card is in the reader's language. Both are one
  // round trip and they run together.
  await Promise.all([loadLanguage(), startSession()]);
  startRouter(root!, routes, notFound);
}

const routes: Route[] = [
  { pattern: '/', render: frontDoor },
  { pattern: '/login', render: (target) => renderAuthPage(target, 'login') },
  { pattern: '/register', render: (target) => renderAuthPage(target, 'register') },
  // Public: the link is opened out of a mail client, quite possibly in
  // a browser that has never signed in here.
  { pattern: '/verify', render: (target, ctx) => renderVerifyPage(target, ctx.query) },
  { pattern: '/settings', render: guarded(renderSettingsPage) },
  { pattern: '/keys', render: guarded(renderKeysPage) },
  { pattern: '/about', render: guarded(renderAboutPage) },
  { pattern: '/admin/*', render: guarded(adminOnly(admin)) },
  { pattern: '/admin', render: guarded(adminOnly(admin)) },
];

// Route guards live here rather than inside each screen, so "which pages need
// a session" is one readable list. The server enforces the same rules on
// every endpoint regardless — this only decides what to draw.
function guarded(render: Route['render']): Route['render'] {
  return (target, ctx) => {
    if (!currentUser()) {
      navigate('/login', { replace: true });
      return;
    }
    return render(target, ctx);
  };
}

// The address itself. Signed in it is the chat; signed out it is whatever the
// operator has put at the front door, which may be the sign-in card, a page
// they wrote, or a sample conversation.
//
// Only this route consults the setting. Every other signed-out route still
// goes straight to sign-in, because a link to /settings is a request for
// something a visitor cannot have.
function frontDoor(target: HTMLElement): void {
  if (currentUser()) {
    renderChatPage(target);
    return;
  }
  if ((siteInfo().landing?.mode ?? 'login') === 'login') {
    navigate('/login', { replace: true });
    return;
  }
  renderLandingPage(target);
}

function adminOnly(render: Route['render']): Route['render'] {
  return (target, ctx) => {
    if (!isAdmin()) {
      showUnauthorizedModal(target);
      return;
    }
    return render(target, ctx);
  };
}

// --- screens ----------------------------------------------------------------

// Fetched when an administrator first opens the backoffice, rather than by
// everyone who loads the chat. It is a quarter of the application's code and
// most people can never reach it, so it is the one screen worth paying a
// round trip for. The router already awaits a render, so nothing else has to
// know this one arrives late.
function admin(target: HTMLElement, ctx: RouteContext): Promise<void> {
  return import('./admin/admin-page').then((screen) => {
    screen.renderAdminPage(target, ctx.path);
  });
}

function notFound(target: HTMLElement, ctx: RouteContext): void {
  if (currentUser()) {
    const shell = renderShell(target);
    notice(shell.body, t('noSuchPage'), t('noSuchPageBody', { path: ctx.path }));
    return;
  }
  clear(target);
  notice(target, t('noSuchPage'), t('noSuchPageBody', { path: ctx.path }));
}

function notice(target: HTMLElement, title: string, body: string): void {
  const wrap = el('div', 'oa-notice');
  wrap.appendChild(el('h2', 'oa-notice-title', title));
  wrap.appendChild(el('p', 'oa-notice-body', body));
  target.appendChild(wrap);
}
