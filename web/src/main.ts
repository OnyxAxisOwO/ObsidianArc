import './styles/base.css';
import './styles/chat.css';
import './styles/workspace.css';
import './styles/app.css';
import './styles/admin.css';

import { renderAboutPage } from './about/about-page';
import { renderAuthPage } from './auth/auth-page';
import { renderVerifyPage } from './auth/verify-page';
import { renderAdminPage } from './admin/admin-page';
import { renderChatPage } from './chat/chat-page';
import { renderLandingPage } from './landing/landing-page';
import { renderSettingsPage } from './settings/settings-page';
import { renderShell } from './app/shell';
import { t } from './i18n';
import { navigate, startRouter, type Route, type RouteContext } from './router';
import { currentUser, isAdmin, siteInfo, start as startSession } from './session';
import { startTheme } from './theme/theme';
import { clear, el } from './ui/dom';

startTheme();

const root = document.getElementById('app');
if (!root) throw new Error('missing #app');

void boot();

async function boot(): Promise<void> {
  await startSession();
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
      notice(target, t('notAvailable'), t('noAdminAccess'));
      return;
    }
    return render(target, ctx);
  };
}

// --- screens ----------------------------------------------------------------

function admin(target: HTMLElement, ctx: RouteContext): void {
  renderAdminPage(target, ctx.path);
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
