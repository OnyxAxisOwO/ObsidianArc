// The application chrome: the header bar from the standalone build, plus the
// account menu it never needed.
//
// Every signed-in screen is drawn inside this, so the brand, the theme
// toggle and the account button sit in the same place whether the user is
// chatting, in settings, or in the admin backoffice.

import { logout, type Account } from '../api/auth';
import { navigate } from '../router';
import { currentUser, forget, persistTheme, siteInfo } from '../session';
import { nextThemeMode, themeMode } from '../theme/theme';
import { ICONS, clear, el, icon, iconButton } from '../ui/dom';
import { dropdown, menuItem } from '../ui/menu';

export interface Shell {
  /** The .oa-workspace root, already appended to the page root. */
  root: HTMLElement;
  header: HTMLElement;
  /** Where a screen puts its own content. */
  body: HTMLElement;
  /** Between the brand and the theme toggle — the model chip lives here. */
  headerSlot: HTMLElement;
  /** Before the brand — the conversation rail toggle lives here. */
  leadingSlot: HTMLElement;
}

export function renderShell(root: HTMLElement): Shell {
  clear(root);

  const workspace = el('div', 'oa-workspace');
  const header = el('div', 'oa-header');

  const leadingSlot = el('span', 'oa-header-leading');
  const headerSlot = el('span', 'oa-header-slot');

  header.appendChild(leadingSlot);
  header.appendChild(el('span', 'oa-brand', siteInfo().name));
  header.appendChild(el('span', 'oa-header-spacer'));
  header.appendChild(headerSlot);
  header.appendChild(themeToggle());

  const account = currentUser();
  if (account) header.appendChild(accountMenu(account).group);

  const body = el('div', 'oa-chat-root');
  workspace.appendChild(header);
  workspace.appendChild(body);
  root.appendChild(workspace);

  return { root: workspace, header, body, headerSlot, leadingSlot };
}

function themeToggle(): HTMLButtonElement {
  const paint = (target: HTMLButtonElement) => {
    clear(target);
    const mode = themeMode();
    target.appendChild(icon(mode === 'dark' ? ICONS.moon : mode === 'light' ? ICONS.sun : ICONS.auto, 17));
  };

  const control = iconButton('oa-icon-btn', ICONS.auto, 'Theme', () => {
    persistTheme(nextThemeMode());
    paint(control);
  }, 17);
  paint(control);
  return control;
}

function accountMenu(account: Account) {
  const trigger = el('button', 'oa-account-btn');
  trigger.type = 'button';
  trigger.appendChild(avatar(account, false));
  trigger.appendChild(el('span', 'oa-account-name', displayName(account)));
  trigger.title = 'Account';

  return dropdown(trigger, (menu, close) => {
    const head = el('div', 'oa-menu-head');
    head.appendChild(avatar(account, true));
    const text = el('div', 'oa-menu-head-text');
    text.appendChild(el('span', 'oa-menu-head-name', displayName(account)));
    text.appendChild(el('span', 'oa-menu-head-sub', account.email || `@${account.username}`));
    head.appendChild(text);
    menu.appendChild(head);

    if (account.group_name) {
      const row = el('div', 'oa-menu-head');
      row.style.borderBottom = 'none';
      row.style.paddingTop = '0';
      row.appendChild(el('span', 'oa-badge', account.group_name));
      if (account.role === 'admin') row.appendChild(el('span', 'oa-badge oa-badge-muted', 'Admin'));
      menu.appendChild(row);
    }

    menu.appendChild(menuItem({
      title: 'Settings',
      leading: icon(ICONS.gear, 14),
      onSelect: () => {
        close();
        navigate('/settings');
      },
    }));

    if (account.role === 'admin') {
      menu.appendChild(menuItem({
        title: 'Administration',
        leading: icon(ICONS.sliders, 14),
        onSelect: () => {
          close();
          navigate('/admin');
        },
      }));
    }

    menu.appendChild(menuItem({
      title: 'Sign out',
      leading: icon(ICONS.logout, 14),
      onSelect: () => {
        close();
        void signOut();
      },
    }));
  });
}

async function signOut(): Promise<void> {
  try {
    await logout();
  } catch {
    // The cookie may already be gone. Either way the local state goes.
  }
  forget();
  navigate('/login', { replace: true });
}

export function displayName(account: Account): string {
  return account.nickname || account.username;
}

export function avatar(account: Account, large: boolean): HTMLElement {
  const node = el('span', `oa-avatar${large ? ' oa-avatar-lg' : ''}`);
  const src = safeAvatar(account.avatar);
  if (src) {
    const image = el('img');
    image.src = src;
    image.alt = '';
    image.draggable = false;
    node.appendChild(image);
    return node;
  }
  node.textContent = initials(displayName(account));
  return node;
}

// An avatar is stored as free text, so it is checked before it becomes an
// img src: a same-origin path or an inline image, nothing that would turn
// every page view into a request to somewhere else.
function safeAvatar(value: string): string | null {
  const trimmed = value.trim();
  if (!trimmed) return null;
  if (trimmed.startsWith('/') && !trimmed.startsWith('//')) return trimmed;
  if (/^data:image\/(?:png|jpeg|webp|gif|avif);base64,[A-Za-z0-9+/]+=*$/.test(trimmed)) return trimmed;
  return null;
}

function initials(name: string): string {
  const trimmed = name.trim();
  if (!trimmed) return '?';
  // Works for a single word and for "First Last"; for CJK, the first
  // character is already the recognisable one.
  const parts = trimmed.split(/\s+/);
  if (parts.length === 1) return [...trimmed][0] ?? '?';
  return `${[...(parts[0] ?? '')][0] ?? ''}${[...(parts[parts.length - 1] ?? '')][0] ?? ''}`;
}
