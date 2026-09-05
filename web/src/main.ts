import './styles/base.css';
import './styles/chat.css';
import './styles/workspace.css';
import './styles/app.css';
import './styles/admin.css';

import { renderAuthPage } from './auth/auth-page';
import { renderAdminPage } from './admin/admin-page';
import { renderChatPage } from './chat/chat-page';
import { renderSettingsPage } from './settings/settings-page';
import { renderShell } from './app/shell';
import { navigate, startRouter, type Route, type RouteContext } from './router';
import { currentUser, isAdmin, start as startSession } from './session';
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
  { pattern: '/', render: guarded(renderChatPage) },
  { pattern: '/login', render: (target) => renderAuthPage(target, 'login') },
  { pattern: '/register', render: (target) => renderAuthPage(target, 'register') },
  { pattern: '/settings', render: guarded(renderSettingsPage) },
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

function adminOnly(render: Route['render']): Route['render'] {
  return (target, ctx) => {
    if (!isAdmin()) {
      notice(target, 'Not available', 'You do not have access to the administration area.');
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
    notice(shell.body, 'Nothing here', `No page matches ${ctx.path}.`);
    return;
  }
  clear(target);
  notice(target, 'Nothing here', `No page matches ${ctx.path}.`);
}

function notice(target: HTMLElement, title: string, body: string): void {
  const wrap = el('div', 'oa-notice');
  wrap.appendChild(el('h2', 'oa-notice-title', title));
  wrap.appendChild(el('p', 'oa-notice-body', body));
  target.appendChild(wrap);
}
