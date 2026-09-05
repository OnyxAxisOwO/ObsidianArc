// Sign in and sign up.
//
// One screen with two modes rather than two pages: the fields and the layout
// are nearly identical, and switching between them should not cost a
// navigation or re-render the card.

import { ApiError } from '../api/client';
import { login, register, type Account } from '../api/auth';
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

  card.appendChild(el('h1', 'oa-auth-title', setup ? 'Create the first account' : registering ? 'Create an account' : 'Welcome back'));
  card.appendChild(el('p', 'oa-auth-sub',
    setup
      ? 'This server has no accounts yet. The first one becomes the administrator.'
      : registering
        ? 'Pick a username and a password. An email address is optional.'
        : 'Sign in to pick up where you left off.'));

  const form = el('div', 'oa-auth-form');
  const errorLine = el('p', 'oa-auth-error');
  errorLine.hidden = true;
  errorLine.setAttribute('role', 'alert');

  const identifier = textInput({
    placeholder: registering ? 'Username' : 'Username or email',
    autocomplete: 'username',
    maxLength: 254,
  });
  form.appendChild(field(registering ? 'Username' : 'Username or email', identifier));

  let email: HTMLInputElement | null = null;
  if (registering) {
    email = textInput({ type: 'email', placeholder: 'you@example.com', autocomplete: 'email', maxLength: 254 });
    form.appendChild(field('Email (optional)', email));
  }

  const password = textInput({
    type: 'password',
    placeholder: registering ? 'At least 8 characters' : 'Password',
    autocomplete: registering ? 'new-password' : 'current-password',
    maxLength: 256,
  });
  form.appendChild(field('Password', password));

  const submit = button('oa-btn primary oa-btn-block', registering ? 'Create account' : 'Sign in');
  submit.type = 'submit';
  form.appendChild(errorLine);
  form.appendChild(submit);
  card.appendChild(form);

  if (!setup) {
    const switcher = el('p', 'oa-auth-switch');
    if (registering) {
      switcher.appendChild(el('span', null, 'Already have an account? '));
      switcher.appendChild(button(null, 'Sign in', () => navigate('/login')));
    } else if (site.registration_enabled) {
      switcher.appendChild(el('span', null, 'No account yet? '));
      switcher.appendChild(button(null, 'Create one', () => navigate('/register')));
    } else {
      switcher.textContent = 'Registration is closed on this server.';
    }
    card.appendChild(switcher);
  }

  if (site.description) card.appendChild(el('p', 'oa-auth-note', site.description));

  // The theme toggle belongs here too: the sign-in page is the first thing a
  // new user sees, and being stuck in the wrong scheme until they have an
  // account would be an odd first impression.
  const themeToggle = iconButton('oa-icon-btn', themeIcon(), 'Theme', () => {
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
      showError('Fill in both fields.');
      return;
    }

    busy = true;
    submit.dataset['busy'] = 'true';
    submit.disabled = true;
    submit.textContent = registering ? 'Creating…' : 'Signing in…';
    errorLine.hidden = true;

    try {
      const result: { user: Account } = registering
        ? await register({
            username: identity,
            password: secret,
            email: email?.value.trim() ?? '',
          })
        : await login(identity, secret);

      adopt(result.user);
      navigate('/', { replace: true });
    } catch (error) {
      showError(error instanceof ApiError ? error.message : String(error));
      busy = false;
      delete submit.dataset['busy'];
      submit.disabled = false;
      submit.textContent = registering ? 'Create account' : 'Sign in';
      password.focus();
      password.select();
    }
  });

  function showError(message: string): void {
    errorLine.textContent = message;
    errorLine.hidden = false;
  }
}

function themeIcon(): readonly string[] {
  const mode = themeMode();
  return mode === 'dark' ? ICONS.moon : mode === 'light' ? ICONS.sun : ICONS.auto;
}
