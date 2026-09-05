import './styles/base.css';
import './styles/chat.css';
import './styles/workspace.css';

import { health } from './api/client';
import { startRouter } from './router';
import { startTheme, nextThemeMode, setThemeMode, themeMode } from './theme/theme';
import { ICONS, clear, el, icon, iconButton } from './ui/dom';

startTheme();

const root = document.getElementById('app');
if (!root) throw new Error('missing #app');

// Phase 1 places the shell and proves the pipeline end to end: theme applied
// before paint, styles bundled, the SPA served by the Go binary, and a live
// call to the API it is served from. Authentication and the chat itself
// replace this screen in the phases that follow.
startRouter(
  root,
  [{ pattern: '/', render: renderStatus }],
  (target) => {
    clear(target);
    target.appendChild(shell(el('p', 'ai-chat-empty-body', 'That page does not exist yet.')));
  },
);

async function renderStatus(target: HTMLElement): Promise<void> {
  clear(target);

  const card = el('div', 'ai-chat-setup');
  card.appendChild(el('h3', 'ai-chat-setup-title', 'Obsidian Arc'));
  const line = el('p', 'ai-chat-setup-body', 'Checking the server…');
  card.appendChild(line);
  target.appendChild(shell(card));

  try {
    const status = await health();
    line.textContent = `Server ${status.version} is up. Sign-in and chat arrive in the next phases.`;
  } catch (error) {
    line.textContent = error instanceof Error ? error.message : String(error);
  }
}

// The header from the standalone build, minus the parts that need a session.
// Kept here so every phase after this one inherits the same chrome rather
// than growing a second one.
function shell(content: HTMLElement): HTMLElement {
  const workspace = el('div', 'oa-workspace');

  const header = el('div', 'oa-header');
  header.appendChild(el('span', 'oa-brand', 'Obsidian Arc'));
  header.appendChild(el('span', 'oa-header-spacer'));

  const themeButton = iconButton('oa-icon-btn', ICONS.auto, 'Theme', () => {
    setThemeMode(nextThemeMode());
    paintThemeIcon(themeButton);
  }, 17);
  paintThemeIcon(themeButton);
  header.appendChild(themeButton);

  const body = el('div', 'oa-chat-root ai-chat ai-chat-wide');
  const main = el('div', 'ai-chat-main');
  const scroll = el('div', 'ai-chat-scroll');
  scroll.appendChild(content);
  main.appendChild(scroll);
  body.appendChild(main);

  workspace.appendChild(header);
  workspace.appendChild(body);
  return workspace;
}

function paintThemeIcon(target: HTMLButtonElement): void {
  clear(target);
  const mode = themeMode();
  target.appendChild(icon(mode === 'dark' ? ICONS.moon : mode === 'light' ? ICONS.sun : ICONS.auto, 17));
}
