// Sign in and sign up.
//
// One screen with two modes rather than two pages: the fields and the layout
// are nearly identical, and switching between them should not cost a
// navigation or re-render the card.

import { ApiError } from '../api/client';
import { challenge } from '../ui/turnstile';
import { login, register, type Account } from '../api/auth';
import { t } from '../i18n';
import { navigate } from '../router';
import { adopt, siteInfo } from '../session';
import { nextThemeMode, themeMode } from '../theme/theme';
import { persistTheme } from '../session';
import { ICONS, button, clear, el, field, icon, iconButton, textInput } from '../ui/dom';

type Mode = 'login' | 'register';

export function renderAuthPage(root: HTMLElement, mode: Mode): void {
  clear(root);

  const site = siteInfo();
  const page = el('div', 'oa-auth');
  const card = el('form', 'oa-auth-card');
  card.noValidate = true;

  const brand = el('div', 'oa-auth-brand');
  const mark = el('span', 'oa-auth-mark');
  mark.appendChild(icon(ICONS.spark, 15));
  brand.appendChild(mark);
  brand.appendChild(el('span', null, site.name));
  card.appendChild(brand);

  // An instance with no accounts is being set up: the person in front of it
  // is about to become the administrator, and saying so removes the "did I
  // just make a normal account?" doubt.
  const setup = site.setup_required;
  const registering = mode === 'register' || setup;

  card.appendChild(el('h1', 'oa-auth-title',
    setup ? t('firstAccountTitle') : registering ? t('createAccountTitle') : t('welcomeBack')));
  card.appendChild(el('p', 'oa-auth-sub',
    setup ? t('firstAccountBody') : registering ? t('createAccountBody') : t('welcomeBackBody')));

  const form = el('div', 'oa-auth-form');
  const errorLine = el('p', 'oa-auth-error');
  errorLine.hidden = true;
  errorLine.setAttribute('role', 'alert');

  const identityLabel = registering ? t('username') : t('usernameOrEmail');
  const identifier = textInput({
    placeholder: identityLabel,
    autocomplete: 'username',
    maxLength: 254,
  });
  form.appendChild(field(identityLabel, identifier));

  const domains = site.email_domains ?? [];
  const emailRequired = !setup && (site.require_email ?? false);

  let email: HTMLInputElement | null = null;
  if (registering) {
    email = textInput({
      type: 'email',
      placeholder: domains.length ? `you@${domains[0]}` : 'you@example.com',
      autocomplete: 'email',
      maxLength: 254,
    });
    email.required = emailRequired;
    // The label carries whether it is optional; the hint carries which
    // addresses will be taken. Both are things you want before typing,
    // not after submitting.
    form.appendChild(field(
      emailRequired ? t('email') : t('emailOptional'),
      email,
      emailHint(domains, !setup && (site.verify_email ?? false)),
    ));
  }

  const qqRequirement = site.qq_requirement ?? (site.require_qq ? 'required' : 'off');
  const qqRequired = !setup && qqRequirement === 'required';
  const qqEnabled = !setup && qqRequirement !== 'off';

  let qqInput: HTMLInputElement | null = null;
  if (registering && qqEnabled) {
    qqInput = textInput({
      type: 'text',
      placeholder: t('qqPlaceholder'),
      autocomplete: 'off',
      maxLength: 15,
    });
    qqInput.required = qqRequired;
    form.appendChild(field(
      qqRequired ? t('qq') : t('qqOptional'),
      qqInput,
    ));
  }

  const password = textInput({
    type: 'password',
    placeholder: registering ? t('passwordHint') : t('password'),
    autocomplete: registering ? 'new-password' : 'current-password',
    maxLength: 256,
  });
  form.appendChild(field(t('password'), password));

  const submit = button('oa-btn primary oa-btn-block', registering ? t('createAccount') : t('signIn'));
  submit.type = 'submit';
  // Only on the sign-up half, and only where the operator switched it on:
  // signing in is not the door bots are trying.
  const guard = registering && site.turnstile_on_signup
    ? challenge(site.turnstile_site_key ?? '')
    : null;
  if (guard) form.appendChild(guard.element);

  form.appendChild(errorLine);
  form.appendChild(submit);
  card.appendChild(form);

  if (!setup) {
    const switcher = el('p', 'oa-auth-switch');
    if (registering) {
      switcher.appendChild(el('span', null, t('haveAccount')));
      switcher.appendChild(button(null, t('signIn'), () => navigate('/login')));
    } else if (site.registration_enabled) {
      switcher.appendChild(el('span', null, t('noAccount')));
      switcher.appendChild(button(null, t('createOne'), () => navigate('/register')));
    } else {
      switcher.textContent = t('registrationClosed');
    }
    card.appendChild(switcher);
  }

  if (site.description) card.appendChild(el('p', 'oa-auth-note', site.description));

  // The theme toggle belongs here too: the sign-in page is the first thing a
  // new user sees, and being stuck in the wrong scheme until they have an
  // account would be an odd first impression.
  const themeToggle = iconButton('oa-icon-btn', themeIcon(), t('theme'), () => {
    persistTheme(nextThemeMode());
    clear(themeToggle);
    themeToggle.appendChild(icon(themeIcon(), 17));
  }, 17);
  const corner = el('div', 'oa-auth-corner');
  corner.appendChild(themeToggle);

  page.appendChild(card);
  root.appendChild(page);
  root.appendChild(corner);

  identifier.focus();

  let busy = false;
  card.addEventListener('submit', async (event) => {
    event.preventDefault();
    if (busy) return;

    const identity = identifier.value.trim();
    const secret = password.value;
    if (!identity || !secret) {
      showError(t('fillBothFields'));
      return;
    }
    // Checked here as well as on the server, only so the answer is
    // immediate. The server is the one that decides.
    if (registering && emailRequired && !email?.value.trim()) {
      showError(t('emailRequiredHere'));
      return;
    }

    const qqVal = qqInput ? qqInput.value.trim() : '';
    if (registering && qqRequired && !qqVal) {
      showError(t('qqRequiredHere'));
      return;
    }
    if (registering && qqVal && !/^[1-9][0-9]{4,14}$/.test(qqVal)) {
      showError(t('qqInvalid'));
      return;
    }

    busy = true;
    submit.dataset['busy'] = 'true';
    submit.disabled = true;
    submit.textContent = registering ? t('creatingAccount') : t('signingIn');
    // A review takes seconds. Saying so beats a button that sits on
    // "creating account" long enough to read as a form that has hung.
    const reviewNote = registering && site.signup_review
      ? window.setTimeout(() => { submit.textContent = t('signupReviewing'); }, 900)
      : 0;
    errorLine.hidden = true;

    try {
      const result: { user: Account } = registering
        ? await register({
            username: identity,
            password: secret,
            email: email?.value.trim() ?? '',
            qq: qqVal,
            turnstile: guard?.token() ?? '',
          })
        : await login(identity, secret);

      adopt(result.user);
      navigate('/', { replace: true });
      guard?.reset();
      window.clearTimeout(reviewNote);
    } catch (error) {
      window.clearTimeout(reviewNote);
      // A token is good for one submission, so a refusal for any reason —
      // a taken username as much as a failed challenge — leaves a spent
      // token behind that would fail the next attempt on its own.
      guard?.reset();
      showError(refusal(error, domains));
      busy = false;
      delete submit.dataset['busy'];
      submit.disabled = false;
      submit.textContent = registering ? t('createAccount') : t('signIn');
      password.focus();
      password.select();
    }
  });

  // The refusals worth saying in the reader's own language. Everything
  // else is a server message with nothing to add.
  function refusal(error: unknown, accepted: string[]): string {
    if (!(error instanceof ApiError)) return String(error);
    switch (error.code) {
      case 'account_banned':
        return t('accountBanned');
      case 'signup_ip_blocked':
        return t('signupBlocked');
      case 'signup_refused': {
        // The operator's own words when they wrote any — a way to appeal is
        // the whole reason to write them — and a plain sentence otherwise.
        const notice = error.details['notice'];
        return typeof notice === 'string' && notice.trim() ? notice : t('signupRefused');
      }
      case 'challenge_failed':
        return t('challengeFailed');
      case 'challenge_unavailable':
        return t('challengeUnavailable');
      case 'qq_required':
        return t('qqRequiredHere');
      case 'invalid_qq':
        return t('qqInvalid');
      case 'qq_taken':
        return t('qqTaken');
      case 'signups_throttled': {
        const seconds = Number(error.details['retry_after_seconds'] ?? 60);
        return t('signupsThrottled', { count: seconds });
      }
      default: {
        const allowed = error.details['allowed_domains'];
        if (Array.isArray(allowed) && allowed.length) {
          return t('emailDomainRejected', { domains: allowed.join(', ') });
        }
        if (accepted.length && error.status === 400 && /email/i.test(error.message)) {
          return t('emailDomainRejected', { domains: accepted.join(', ') });
        }
        return error.message;
      }
    }
  }

  function showError(message: string): void {
    errorLine.textContent = message;
    errorLine.hidden = false;
  }
}

// What to say under the address field: which domains are taken, that a
// link is coming, or both. Nothing when neither applies.
function emailHint(domains: string[], verifying: boolean): string | undefined {
  const parts: string[] = [];
  if (domains.length) parts.push(t('emailAccepted', { domains: domains.join(', ') }));
  if (verifying) parts.push(t('verifySignupNote'));
  return parts.length ? parts.join(' ') : undefined;
}

function themeIcon(): readonly string[] {
  const mode = themeMode();
  return mode === 'dark' ? ICONS.moon : mode === 'light' ? ICONS.sun : ICONS.auto;
}
