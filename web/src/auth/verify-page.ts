// The page a verification link lands on.
//
// It has to work for someone who is not signed in — the link is opened out of
// a mail client, quite possibly in a different browser from the one that
// registered — so it does its own request and never assumes a session.

import { ApiError } from '../api/client';
import { verifyEmail } from '../api/auth';
import { t } from '../i18n';
import { navigate } from '../router';
import { siteInfo } from '../session';
import { ICONS, button, clear, el, icon } from '../ui/dom';

export function renderVerifyPage(root: HTMLElement, query: URLSearchParams): void {
  clear(root);

  const page = el('div', 'oa-auth');
  const card = el('div', 'oa-auth-card');

  const brand = el('div', 'oa-auth-brand');
  const mark = el('span', 'oa-auth-mark');
  mark.appendChild(icon(ICONS.spark, 15));
  brand.appendChild(mark);
  brand.appendChild(el('span', null, siteInfo().name));
  card.appendChild(brand);

  const title = el('h1', 'oa-auth-title', t('verifyPageChecking'));
  const body = el('p', 'oa-auth-sub');
  card.appendChild(title);
  card.appendChild(body);

  const action = button('oa-btn primary oa-btn-block', t('verifyPageContinue'), () => {
    navigate('/', { replace: true });
  });
  action.hidden = true;
  card.appendChild(action);

  page.appendChild(card);
  root.appendChild(page);

  void verifyEmail(query.get('token') ?? '')
    .then(() => {
      title.textContent = t('verifyPageDone');
      body.textContent = '';
      action.hidden = false;
      action.focus();
    })
    .catch((error: unknown) => {
      title.textContent = t('verifyPageFailedTitle');
      body.textContent = error instanceof ApiError ? error.message : String(error);
      // Still somewhere to go: the link may have already been used, in which
      // case the account is fine and signing in is the right next step.
      action.textContent = t('signIn');
      action.hidden = false;
    });
}
