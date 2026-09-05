// Instance settings.
//
// A short list, because every entry here is a decision an operator has to
// understand before they change it. Anything with a sensible answer for every
// deployment is a constant, not a setting.

import { ApiError } from '../api/client';
import { pickJSONFile, saveAsFile } from '../api/backup';
import { t } from '../i18n';
import { button, clear, el } from '../ui/dom';
import { numberField, section, selectField, switchField, textArea, textField } from '../ui/form';
import { adminApi, type AdminModel } from './api';
import { failure, type AdminView } from './admin-page';

export async function renderSettings(view: AdminView): Promise<void> {
  view.setTitle(t('adminSettingsTitle'));

  let data;
  let models: AdminModel[] = [];
  try {
    // The models come along because one of these settings is which model
    // answers a trial, and a select needs its options.
    [data, { models }] = await Promise.all([adminApi.settings(), adminApi.models()]);
  } catch (error) {
    failure(view, error);
    return;
  }

  const values = data.settings;

  const siteName = textField({
    label: t('siteName'),
    value: values['site.name'] ?? '',
    hint: t('siteNameHint'),
    maxLength: 60,
  });

  const description = textArea({
    label: t('signInNote'),
    value: values['site.description'] ?? '',
    rows: 2,
    hint: t('signInNoteHint'),
  });

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

  const adminBypass = switchField({
    label: t('adminsIgnoreLimits'),
    value: values['quota.admins_bypass'] === 'true',
    hint: t('adminsIgnoreLimitsHint'),
  });

  const usageDisplay = selectField({
    label: t('usageDisplay'),
    value: values['quota.usage_display'] ?? 'absolute',
    hint: t('usageDisplayHint'),
    options: [
      { value: 'absolute', label: t('usageDisplayAbsolute') },
      { value: 'remaining', label: t('usageDisplayRemaining') },
      { value: 'used', label: t('usageDisplayUsed') },
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
  // Offered but inert without SMTP, and the hint above says so. The
  // server ignores it in that state too, so an operator cannot lock
  // every new account out of an instance that cannot send the link.
  verifyEmail.element.classList.toggle('oa-field-inert', !data.mail_configured);

  const emailDomains = textArea({
    label: t('emailDomains'),
    value: values['registration.email_domains'] ?? '',
    rows: 2,
    placeholder: t('emailDomainsPlaceholder'),
    hint: t('emailDomainsHint'),
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

  const landingMode = selectField({
    label: t('landingMode'),
    value: values['landing.mode'] ?? 'login',
    hint: t('landingModeHint'),
    options: [
      { value: 'login', label: t('landingLogin') },
      { value: 'intro', label: t('landingIntro') },
      { value: 'chat', label: t('landingChat') },
    ],
    onChange: () => paintLanding(),
  });

  const landingIntro = textArea({
    label: t('landingIntroHTML'),
    value: values['landing.intro'] ?? '',
    rows: 8,
    hint: t('landingIntroHTMLHint'),
  });

  const trialEnabled = switchField({
    label: t('trialEnabled'),
    value: values['landing.trial_enabled'] === 'true',
    hint: t('trialEnabledHint'),
    onChange: () => paintLanding(),
  });

  const trialTurns = numberField({
    label: t('trialTurns'),
    value: Number(values['landing.trial_turns'] ?? 3),
    min: 1,
    max: 20,
    hint: t('trialTurnsHint', { max: 20 }),
  });

  const trialModel = selectField({
    label: t('trialModel'),
    value: values['landing.trial_model'] ?? '',
    options: [
      { value: '', label: t('trialFirstAvailable') },
      ...models
        .filter((entry) => entry.enabled)
        .map((entry) => ({ value: entry.id, label: entry.display_name })),
    ],
  });

  const systemPrompt = textArea({
    label: t('instanceSystemPrompt'),
    value: values['chat.default_system_prompt'] ?? '',
    rows: 4,
    hint: t('instanceSystemPromptHint'),
  });

  const attachmentMaxMB = numberField({
    label: t('attachmentMaxMB'),
    value: Number(values['attachments.max_mb'] ?? 6),
    min: 1,
    max: 64,
    hint: t('attachmentMaxMBHint'),
  });

  const attachmentRetain = switchField({
    label: t('attachmentRetain'),
    value: values['attachments.retain'] === 'true',
    hint: t('attachmentRetainHint'),
  });

  const apiEnabled = switchField({
    label: t('apiEnabled'),
    value: values['api.enabled'] === 'true',
    hint: t('apiEnabledHint'),
  });

  const maxTurns = numberField({
    label: t('turnsResent'),
    value: Number(values['chat.max_turns'] ?? 40),
    min: 2,
    max: 200,
    hint: t('turnsResentHint'),
  });

  clear(view.actions);
  // Export takes what the form is showing, not what was last saved: an
  // operator who has just typed a value expects the file to contain it.
  const download = button('oa-btn', t('exportSettings'), () => {
    const stamp = new Date().toISOString().slice(0, 10);
    saveAsFile(`obsidian-arc-settings-${stamp}.json`, JSON.stringify(collect(), null, 2));
  });

  const upload = button('oa-btn', t('importSettings'), () => {
    void pickJSONFile(1024 * 1024)
      .then((document) => {
        if (document === null) return null;
        if (!document || typeof document !== 'object' || Array.isArray(document)) {
          throw new ApiError(0, 'malformed', t('importSettingsMalformed'));
        }
        // Everything is stored as a string; a file written by hand may well
        // carry numbers and booleans, and refusing those would be pedantry.
        const values: Record<string, string> = {};
        for (const [key, value] of Object.entries(document as Record<string, unknown>)) {
          if (value !== null && typeof value !== 'object') values[key] = String(value);
        }
        upload.disabled = true;
        return adminApi.importSettings(values);
      })
      .then((result) => {
        if (!result) return;
        flash.textContent = result.skipped.length
          ? t('importSettingsPartial', { count: result.applied, skipped: result.skipped.join(', ') })
          : t('importSettingsDone', { count: result.applied });
        flash.classList.add('visible');
        // The form is now describing values that are no longer current.
        void renderSettings(view);
      })
      .catch((error: unknown) => {
        flash.textContent = error instanceof ApiError ? error.message : String(error);
        flash.classList.add('visible');
      })
      .finally(() => { upload.disabled = false; });
  });

  const save = button('oa-btn primary', t('save'), () => void submit());
  view.actions.appendChild(download);
  view.actions.appendChild(upload);
  view.actions.appendChild(save);

  clear(view.body);
  const form = el('div', 'oa-drawer-body');
  form.style.padding = '0';
  form.style.overflow = 'visible';

  form.appendChild(section(t('secIdentity')));
  form.appendChild(siteName.element);
  form.appendChild(description.element);

  form.appendChild(section(t('secAccounts')));
  form.appendChild(registration.element);
  form.appendChild(defaultGroup.element);

  form.appendChild(section(t('secRegistration')));
  form.appendChild(requireEmail.element);
  form.appendChild(verifyEmail.element);
  form.appendChild(emailDomains.element);
  form.appendChild(perMinute.element);
  form.appendChild(perHour.element);

  form.appendChild(section(t('secLanding')));
  form.appendChild(landingMode.element);
  form.appendChild(landingIntro.element);
  form.appendChild(trialEnabled.element);
  form.appendChild(trialTurns.element);
  form.appendChild(trialModel.element);
  paintLanding();

  form.appendChild(section(t('secChat')));
  form.appendChild(systemPrompt.element);
  form.appendChild(maxTurns.element);

  form.appendChild(section(t('secLimits')));
  form.appendChild(adminBypass.element);
  form.appendChild(usageDisplay.element);

  form.appendChild(section(t('secAttachments'), t('attachmentsHint')));
  form.appendChild(attachmentMaxMB.element);
  form.appendChild(attachmentRetain.element);

  form.appendChild(section(t('apiKeys')));
  form.appendChild(apiEnabled.element);

  const flash = el('p', 'oa-drawer-flash');
  form.appendChild(flash);
  view.body.appendChild(form);

  // The three landing modes want different fields, and showing all of
  // them at once invites setting a trial on a front door that is a
  // sign-in card.
  function paintLanding(): void {
    const mode = landingMode.value();
    landingIntro.element.hidden = mode !== 'intro';
    trialEnabled.element.hidden = mode !== 'chat';
    const live = mode === 'chat' && trialEnabled.value();
    trialTurns.element.hidden = !live;
    trialModel.element.hidden = !live;
  }

  // The one description of what this form holds, so a save and an export
  // cannot come to disagree about it.
  function collect(): Record<string, string> {
    return {
      'site.name': siteName.value(),
      'site.description': description.value(),
      'registration.enabled': String(registration.value()),
      'registration.default_group': defaultGroup.value(),
      'registration.require_email': String(requireEmail.value()),
      'registration.verify_email': String(verifyEmail.value()),
      'registration.email_domains': emailDomains.value(),
      'registration.per_minute': String(perMinute.value() ?? 0),
      'registration.per_hour': String(perHour.value() ?? 0),
      'landing.mode': landingMode.value(),
      'landing.intro': landingIntro.value(),
      'landing.trial_enabled': String(trialEnabled.value()),
      'landing.trial_turns': String(trialTurns.value() ?? 3),
      'landing.trial_model': trialModel.value(),
      'quota.admins_bypass': String(adminBypass.value()),
      'quota.usage_display': usageDisplay.value(),
      'chat.default_system_prompt': systemPrompt.value(),
      'chat.max_turns': String(maxTurns.value() ?? 40),
      'api.enabled': String(apiEnabled.value()),
      'attachments.max_mb': String(attachmentMaxMB.value() ?? 6),
      'attachments.retain': String(attachmentRetain.value()),
    };
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
