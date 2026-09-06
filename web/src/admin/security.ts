// Everything between the front door and an account.
//
// Split out of the settings screen because these are one subject and it was
// not: whether registration is open, what a new account must supply, how fast
// addresses may open them, whether a browser has to prove itself, and whether
// a model is asked to look at the result. An operator dealing with a wave of
// junk accounts opens one page, not seven sections of another.

import { ApiError } from '../api/client';
import { t } from '../i18n';
import { button, clear, el } from '../ui/dom';
import { numberField, section, selectField, switchField, textArea, textField } from '../ui/form';
import { adminApi, type AdminModel } from './api';
import { failure, type AdminView } from './admin-page';

export async function renderSecurity(view: AdminView): Promise<void> {
  view.setTitle(t('navSecurity'), t('securitySubtitle'));

  let data;
  let models: AdminModel[] = [];
  try {
    // The models come along because one of these settings is which model
    // reviews a sign-up, and a select needs its options. The groups arrive
    // with the settings already.
    [data, { models }] = await Promise.all([adminApi.settings(), adminApi.models()]);
  } catch (error) {
    failure(view, error);
    return;
  }

  const values = data.settings;

  const registration = switchField({
    label: t('anyoneCanRegister'),
    value: values['registration.enabled'] === 'true',
    hint: t('anyoneCanRegisterHint'),
  });

  const defaultGroup = selectField({
    label: t('newAccountsJoin'),
    value: values['registration.default_group'] ?? '',
    options: [
      { value: '', label: t('theDefaultGroup') },
      ...data.groups.map((group) => ({ value: group.id, label: group.name })),
    ],
  });

  const requireEmail = switchField({
    label: t('requireEmail'),
    value: values['registration.require_email'] === 'true',
    hint: t('requireEmailHint'),
  });

  const verifyEmail = switchField({
    label: t('verifyEmail'),
    value: values['registration.verify_email'] === 'true',
    hint: data.mail_configured ? t('verifyEmailHint') : t('verifyEmailNoMail'),
  });

  const emailDomains = textArea({
    label: t('emailDomains'),
    value: values['registration.email_domains'] ?? '',
    rows: 2,
    placeholder: t('emailDomainsPlaceholder'),
    hint: t('emailDomainsHint'),
  });

  const qqRequirement = selectField({
    label: t('qqRequirement'),
    value: values['registration.qq_requirement'] ?? 'off',
    hint: t('qqRequirementHint'),
    options: [
      { value: 'off', label: t('qqRequirementOff') },
      { value: 'optional', label: t('qqRequirementOptional') },
      { value: 'required', label: t('qqRequirementRequired') },
    ],
  });

  const perMinute = numberField({
    label: t('signupsPerMinute'),
    value: Number(values['registration.per_minute'] ?? 0),
    min: 0,
    max: 1000,
  });

  const perHour = numberField({
    label: t('signupsPerHour'),
    value: Number(values['registration.per_hour'] ?? 0),
    min: 0,
    max: 10000,
    hint: t('signupThrottleHint'),
  });

  const signupsPerIP = numberField({
    label: t('signupsPerIP'),
    value: Number(values['registration.per_ip'] ?? 0),
    min: 0,
    hint: t('signupsPerIPHint'),
  });

  const signupsIPWindow = numberField({
    label: t('signupsIPWindow'),
    value: Number(values['registration.per_ip_window_minutes'] ?? 60),
    min: 1,
    hint: t('signupsIPWindowHint'),
  });

  const turnstileSiteKey = textField({
    label: t('turnstileSiteKey'),
    value: values['turnstile.site_key'] ?? '',
    placeholder: '0x4AAAAAAA…',
    hint: t('turnstileSiteKeyHint'),
    monospace: true,
  });

  const turnstileSecret = textField({
    label: t('turnstileSecretKey'),
    value: '',
    placeholder: values['turnstile.secret_key'] ? values['turnstile.secret_key'] : '0x4AAAAAAA…',
    hint: t('turnstileSecretHint'),
    monospace: true,
  });

  const turnstileOnSignup = switchField({
    label: t('turnstileOnSignup'),
    value: values['turnstile.on_signup'] === 'true',
    hint: t('turnstileOnSignupHint'),
  });

  const turnstileOnAPIKey = switchField({
    label: t('turnstileOnAPIKey'),
    value: values['turnstile.on_api_key'] === 'true',
    hint: t('turnstileOnAPIKeyHint'),
  });

  // Offered but inert without SMTP, and the hint above says so. The server
  // ignores it in that state too, so an operator cannot lock every new
  // account out of an instance that cannot send the link.
  verifyEmail.element.classList.toggle('oa-field-inert', !data.mail_configured);

  const reviewEnabled = switchField({
    label: t('signupReview'),
    value: values['security.signup_review'] === 'true',
    hint: t('signupReviewHint'),
  });

  const reviewModel = selectField({
    label: t('signupReviewModel'),
    value: values['security.signup_review_model'] ?? '',
    options: [
      { value: '', label: t('signupReviewNoModel') },
      ...models.filter((entry) => entry.enabled).map((entry) => ({
        value: entry.id, label: entry.display_name,
      })),
    ],
    hint: t('signupReviewModelHint'),
  });

  const reviewMode = selectField({
    label: t('signupReviewMode'),
    value: values['security.signup_review_mode'] ?? 'normal',
    options: [
      { value: 'loose', label: t('reviewModeLoose') },
      { value: 'normal', label: t('reviewModeNormal') },
      { value: 'strict', label: t('reviewModeStrict') },
    ],
    hint: t('signupReviewModeHint'),
  });

  const reviewRefusal = textArea({
    label: t('signupReviewRefusal'),
    value: values['security.signup_review_refusal'] ?? '',
    rows: 3,
    placeholder: t('signupReviewRefusalPlaceholder'),
    hint: t('signupReviewRefusalHint'),
  });

  const form = el('div', 'oa-settings-panel');

  form.appendChild(section(t('secAccounts')));
  form.appendChild(registration.element);
  form.appendChild(defaultGroup.element);

  form.appendChild(section(t('secRegistration')));
  form.appendChild(requireEmail.element);
  form.appendChild(verifyEmail.element);
  form.appendChild(emailDomains.element);
  form.appendChild(qqRequirement.element);
  form.appendChild(perMinute.element);
  form.appendChild(perHour.element);
  form.appendChild(signupsPerIP.element);
  form.appendChild(signupsIPWindow.element);

  form.appendChild(section(t('secTurnstile'), t('turnstileHint')));
  form.appendChild(turnstileSiteKey.element);
  form.appendChild(turnstileSecret.element);
  form.appendChild(turnstileOnSignup.element);
  form.appendChild(turnstileOnAPIKey.element);

  form.appendChild(section(t('secSignupReview'), t('signupReviewIntro')));
  form.appendChild(reviewEnabled.element);
  form.appendChild(reviewModel.element);
  form.appendChild(reviewMode.element);
  form.appendChild(reviewRefusal.element);
  form.appendChild(trial());

  const flash = el('p', 'oa-drawer-flash');
  form.appendChild(flash);

  const save = button('oa-btn primary', t('save'), () => void submit());
  clear(view.actions);
  view.actions.appendChild(save);

  clear(view.body);
  view.body.appendChild(form);

  /**
   * Only this page's keys. The settings endpoint writes what it is given and
   * leaves the rest alone, which is what lets two screens edit one store
   * without either one reverting the other's fields.
   */
  function collect(): Record<string, string> {
    return {
      'registration.enabled': String(registration.value()),
      'registration.default_group': defaultGroup.value(),
      'registration.require_email': String(requireEmail.value()),
      'registration.verify_email': String(verifyEmail.value()),
      'registration.email_domains': emailDomains.value(),
      'registration.qq_requirement': qqRequirement.value(),
      'registration.per_minute': String(perMinute.value() ?? 0),
      'registration.per_hour': String(perHour.value() ?? 0),
      'registration.per_ip': String(signupsPerIP.value() ?? 0),
      'registration.per_ip_window_minutes': String(signupsIPWindow.value() ?? 60),
      'turnstile.site_key': turnstileSiteKey.value(),
      // Empty keeps what is stored: the field was never shown the secret, so
      // sending its emptiness back would erase it.
      'turnstile.secret_key': turnstileSecret.value(),
      'turnstile.on_signup': String(turnstileOnSignup.value()),
      'turnstile.on_api_key': String(turnstileOnAPIKey.value()),
      'security.signup_review': String(reviewEnabled.value()),
      'security.signup_review_model': reviewModel.value(),
      'security.signup_review_mode': reviewMode.value(),
      'security.signup_review_refusal': reviewRefusal.value(),
    };
  }

  /**
   * Trying the reviewer on an account that is not being created.
   *
   * It exists because "the review is not working" and "the review is working
   * and being generous" look identical from outside: both are a registration
   * that went through. Type the account that got past it and read what the
   * model actually said — including that it could not be reached, which is
   * the state in which everything is allowed.
   */
  function trial(): HTMLElement {
    const wrap = el('div', 'oa-field');
    wrap.appendChild(el('span', 'oa-field-label', t('reviewTry')));
    wrap.appendChild(el('span', 'oa-field-hint', t('reviewTryHint')));

    const username = textField({ label: t('username'), placeholder: '123123123123' });
    const email = textField({ label: t('email'), placeholder: '123123123123@qq.com' });
    const qq = textField({ label: t('qq'), placeholder: '123123123123' });
    const answer = el('p', 'oa-field-hint');

    const run = button('oa-btn', t('reviewTryRun'), () => {
      run.disabled = true;
      answer.textContent = t('reviewTrying');
      void adminApi.tryReview({
        username: username.value(), email: email.value(), qq: qq.value(),
        // What a browser would have sent, so the answer is about the details
        // and not about a missing user agent.
        user_agent: navigator.userAgent,
      })
        .then((result) => {
          answer.textContent = !result.ran
            ? t('reviewTryBroken', { reason: result.reason })
            : t(result.allow ? 'reviewTryAllowed' : 'reviewTryRefused', { reason: result.reason });
        })
        .catch((error: unknown) => {
          answer.textContent = error instanceof ApiError ? error.message : String(error);
        })
        .finally(() => { run.disabled = false; });
    });

    wrap.appendChild(username.element);
    wrap.appendChild(email.element);
    wrap.appendChild(qq.element);
    wrap.appendChild(run);
    wrap.appendChild(answer);
    return wrap;
  }

  async function submit(): Promise<void> {
    save.disabled = true;
    save.textContent = t('saving');
    flash.classList.remove('visible');

    try {
      await adminApi.saveSettings(collect());
      save.textContent = t('saved');
      window.setTimeout(() => { save.textContent = t('save'); }, 1500);
    } catch (error) {
      flash.textContent = error instanceof ApiError ? error.message : String(error);
      flash.classList.add('visible');
      save.textContent = t('save');
    } finally {
      save.disabled = false;
    }
  }
}
