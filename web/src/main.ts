import './styles/base.css';
import './styles/chat.css';
import './styles/workspace.css';
import './styles/app.css';

import { renderAuthPage } from './auth/auth-page';
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
  { pattern: '/', render: guarded(renderChat) },
  { pattern: '/login', render: (target) => renderAuthPage(target, 'login') },
  { pattern: '/register', render: (target) => renderAuthPage(target, 'register') },
  { pattern: '/settings', render: guarded(renderSettings) },
  { pattern: '/admin/*', render: guarded(adminOnly(renderAdmin)) },
  { pattern: '/admin', render: guarded(adminOnly(renderAdmin)) },
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

// Phase 4 replaces this with the ported chat surface. Until then the shell is
// real and the transcript is a placeholder, so the header, account menu and
// theme are all exercised from the start.
function renderChat(target: HTMLElement): void {
  const shell = renderShell(target);
  shell.body.classList.add('ai-chat', 'ai-chat-wide');

  const main = el('div', 'ai-chat-main');
  const scroll = el('div', 'ai-chat-scroll');
  const empty = el('div', 'ai-chat-empty');
  empty.appendChild(el('h3', 'ai-chat-empty-title', `Hello, ${currentUser()?.nickname || currentUser()?.username}`));
  empty.appendChild(el('p', 'ai-chat-empty-body',
    'The chat itself arrives with providers and models, in the next phases. Signing in, the account menu and the theme already work.'));
  scroll.appendChild(empty);
  main.appendChild(scroll);
  shell.body.appendChild(main);
  shell.body.classList.add('is-empty');
}

function renderSettings(target: HTMLElement): void {
  const shell = renderShell(target);
  notice(shell.body, 'Settings', 'Profile, default model and appearance settings arrive in a later phase.');
}

function renderAdmin(target: HTMLElement): void {
  const shell = renderShell(target);
  notice(shell.body, 'Administration', 'Users, groups, providers, models and usage arrive in a later phase.');
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
