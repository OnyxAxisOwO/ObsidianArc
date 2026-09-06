import { logout } from '../api/auth';
import { renderChatPage } from '../chat/chat-page';
import { t } from '../i18n';
import { navigate, onBeforeRender } from '../router';
import { forget, siteInfo } from '../session';
import { ICONS, button, el, icon, iconButton } from '../ui/dom';

let activeClose: ((navBack?: boolean) => void) | null = null;

// A navigation takes the modal with it. Registered rather than left to the
// router, which would only be able to remove the node: the document-level key
// handler and the reference above are the other half of this modal, and an
// Escape that outlives its card navigates from whatever screen is up instead.
onBeforeRender(() => closeUnauthorizedModal());

export function showUnauthorizedModal(root: HTMLElement): void {
  // Reached directly rather than from inside the app: draw the chat behind it
  // so the refusal sits over the product rather than over a boot spinner that
  // will now never resolve.
  if (!root.querySelector('.oa-workspace')) {
    renderChatPage(root);
  }

  // Close whatever is up first — listeners and module state included — then
  // drop its node, so the two cards do not overlap for the length of a fade.
  closeUnauthorizedModal();
  document.querySelector('.oa-modal-overlay')?.remove();

  const overlay = el('div', 'oa-modal-overlay');
  const card = el('div', 'oa-auth-card oa-modal-card');

  const closeBtn = iconButton('oa-icon-btn oa-modal-close', ICONS.close, t('close'), () => close(), 16);
  card.appendChild(closeBtn);

  // The same mark, title and switcher as the sign-in card: this is the same
  // door, answering differently.
  const site = siteInfo();
  const brand = el('div', 'oa-auth-brand');
  const mark = el('span', 'oa-auth-mark');
  mark.appendChild(icon(ICONS.lock, 15));
  brand.appendChild(mark);
  brand.appendChild(el('span', null, site.name));
  card.appendChild(brand);

  card.appendChild(el('h1', 'oa-auth-title', t('accessDenied')));
  card.appendChild(el('p', 'oa-auth-sub', t('noAdminAccess')));

  const form = el('div', 'oa-auth-form');
  const backBtn = button('oa-btn primary oa-btn-block', t('backToChat'), () => close());
  form.appendChild(backBtn);
  card.appendChild(form);

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

/** Dismisses it without moving anywhere: the caller decides where next. */
export function closeUnauthorizedModal(): void {
  activeClose?.(false);
}
