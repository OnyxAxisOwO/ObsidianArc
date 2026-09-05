import { logout } from '../api/auth';
import { renderChatPage } from '../chat/chat-page';
import { t } from '../i18n';
import { navigate } from '../router';
import { forget, siteInfo } from '../session';
import { ICONS, button, el, icon, iconButton } from '../ui/dom';

let activeClose: (() => void) | null = null;

export function showUnauthorizedModal(root: HTMLElement): void {
  // If the root doesn't contain an active workspace (e.g. boot spinner or blank screen on direct load),
  // render the chat shell in the background so there is no stuck loading state.
  if (!root.querySelector('.oa-workspace')) {
    renderChatPage(root);
  }

  // Remove any previously open modal
  activeClose?.();
  document.querySelector('.oa-modal-overlay')?.remove();

  const overlay = el('div', 'oa-modal-overlay');
  const card = el('div', 'oa-auth-card oa-modal-card');

  // Close button in upper right corner
  const closeBtn = iconButton('oa-icon-btn oa-modal-close', ICONS.close, t('close'), () => close(), 16);
  card.appendChild(closeBtn);

  // Brand header matching the login card
  const site = siteInfo();
  const brand = el('div', 'oa-auth-brand');
  const mark = el('span', 'oa-auth-mark');
  mark.appendChild(icon(ICONS.lock, 15));
  brand.appendChild(mark);
  brand.appendChild(el('span', null, site.name));
  card.appendChild(brand);

  // Title and subtitle
  card.appendChild(el('h1', 'oa-auth-title', t('accessDenied')));
  card.appendChild(el('p', 'oa-auth-sub', t('noAdminAccess')));

  // Main action
  const form = el('div', 'oa-auth-form');
  const backBtn = button('oa-btn primary oa-btn-block', t('backToChat'), () => close());
  form.appendChild(backBtn);
  card.appendChild(form);

  // Switcher matching the login card switcher
  const switcher = el('p', 'oa-auth-switch');
  switcher.appendChild(el('span', null, t('needDifferentAccount')));
  switcher.appendChild(button(null, t('switchAccount'), () => {
    void logout().finally(() => {
      forget();
      close(false);
      navigate('/login');
    });
  }));
  card.appendChild(switcher);

  overlay.appendChild(card);
  document.body.appendChild(overlay);

  requestAnimationFrame(() => overlay.classList.add('open'));

  function close(navBack = true): void {
    if (activeClose === close) activeClose = null;
    document.removeEventListener('keydown', onKey);
    overlay.classList.remove('open');
    window.setTimeout(() => overlay.remove(), 200);
    if (navBack) {
      navigate('/', { replace: true });
    }
  }

  activeClose = close;

  function onKey(event: KeyboardEvent): void {
    if (event.key === 'Escape') close();
  }
  document.addEventListener('keydown', onKey);

  overlay.addEventListener('click', (event) => {
    if (event.target === overlay) close();
  });

  backBtn.focus();
}

export function closeUnauthorizedModal(): void {
  activeClose?.();
}
