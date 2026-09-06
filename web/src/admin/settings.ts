// Instance settings.
//
// A short list, because every entry here is a decision an operator has to
// understand before they change it. Anything with a sensible answer for every
// deployment is a constant, not a setting.

import { ApiError } from '../api/client';
import { pickJSONFile, saveAsFile } from '../api/backup';
import { t } from '../i18n';
import { button, clear, confirmable, el } from '../ui/dom';
import { numberField, section, selectField, switchField, textArea, textField } from '../ui/form';
import { adminApi, type AdminModel, type HeldAttachments } from './api';
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

  // The About panel, in the operator's own words. Both empty is the normal
  // state and means the panel keeps its built-in wording.
  const aboutHeading = textField({
    label: t('aboutHeading'),
    value: values['about.title'] ?? '',
    hint: t('aboutHeadingHint'),
    maxLength: 60,
  });

  const aboutText = textArea({
    label: t('aboutText'),
    value: values['about.body'] ?? '',
    rows: 4,
    hint: t('aboutTextHint'),
  });

  // The standing strip above the chat. Empty is the normal state.
  const homeNotice = textArea({
    label: t('homeNotice'),
    value: values['home.notice'] ?? '',
    rows: 3,
    hint: t('homeNoticeHint'),
  });

  const homeNoticeDismissible = switchField({
    label: t('homeNoticeDismissible'),
    value: (values['home.notice_dismissible'] ?? 'true') === 'true',
    hint: t('homeNoticeDismissibleHint'),
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

  const healthProbe = switchField({
    label: t('healthProbe'),
    value: values['health.probe'] !== 'false',
    hint: t('healthProbeHint'),
  });
  const healthWindow = numberField({
    label: t('healthWindow'),
    value: Number(values['health.window_minutes'] ?? 30),
    min: 1,
    hint: t('healthWindowHint'),
  });
  const healthDisableAfter = numberField({
    label: t('healthDisableAfter'),
    value: Number(values['health.disable_after'] ?? 0),
    min: 0,
    hint: t('healthDisableAfterHint'),
  });
  const healthRetainDays = numberField({
    label: t('healthRetainDays'),
    value: Number(values['health.retain_days'] ?? 14),
    min: 1,
    hint: t('healthRetainDaysHint'),
  });

  const healthDisableBelow = numberField({
    label: t('healthDisableBelow'),
    value: Number(values['health.disable_below'] ?? 0),
    min: 0,
    max: 100,
    hint: t('healthDisableBelowHint'),
  });
  const healthShowUsers = switchField({
    label: t('healthShowUsers'),
    value: values['health.show_users'] === 'true',
    hint: t('healthShowUsersHint'),
  });
  const healthWarnBelow = numberField({
    label: t('healthWarnBelow'),
    value: Number(values['health.warn_below'] ?? 90),
    min: 0,
    max: 100,
    hint: t('healthWarnBelowHint'),
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

  const purgeAfterDays = numberField({
    label: t('attachmentPurgeDays'),
    value: Number(values['attachments.purge_after_days'] ?? 0),
    min: 0,
    max: 3650,
    hint: t('attachmentPurgeDaysHint'),
  });

  const purgeDailyAt = textField({
    label: t('attachmentPurgeDaily'),
    value: values['attachments.purge_daily_at'] ?? '',
    placeholder: '03:00',
    hint: t('attachmentPurgeDailyHint'),
    maxLength: 5,
  });

  const orphanMinutes = numberField({
    label: t('attachmentOrphanMinutes'),
    value: Number(values['attachments.orphan_minutes'] ?? 60),
    min: 5,
    max: 1440,
    hint: t('attachmentOrphanMinutesHint'),
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
  form.appendChild(aboutHeading.element);
  form.appendChild(aboutText.element);
  form.appendChild(homeNotice.element);
  form.appendChild(homeNoticeDismissible.element);

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

  form.appendChild(section(t('secCleanup'), t('cleanupHint')));
  form.appendChild(purgeAfterDays.element);
  form.appendChild(purgeDailyAt.element);
  form.appendChild(orphanMinutes.element);
  form.appendChild(heldPanel(data.attachments));

  form.appendChild(section(t('secLiveness'), t('livenessHint')));
  form.appendChild(healthProbe.element);
  form.appendChild(healthWindow.element);
  form.appendChild(healthDisableAfter.element);
  form.appendChild(healthDisableBelow.element);
  form.appendChild(healthWarnBelow.element);
  form.appendChild(healthShowUsers.element);
  form.appendChild(healthRetainDays.element);

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
      'about.title': aboutHeading.value(),
      'about.body': aboutText.value(),
      'home.notice': homeNotice.value(),
      'home.notice_dismissible': String(homeNoticeDismissible.value()),
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
      // Empty keeps what is stored: the field was never shown the secret, so
      // sending its emptiness back would erase it.
      'attachments.max_mb': String(attachmentMaxMB.value() ?? 6),
      'attachments.retain': String(attachmentRetain.value()),
      'attachments.purge_after_days': String(purgeAfterDays.value() ?? 0),
      'attachments.purge_daily_at': purgeDailyAt.value(),
      'attachments.orphan_minutes': String(orphanMinutes.value() ?? 60),
      'health.probe': String(healthProbe.value()),
      'health.window_minutes': String(healthWindow.value() ?? 30),
      'health.disable_after': String(healthDisableAfter.value() ?? 0),
      'health.retain_days': String(healthRetainDays.value() ?? 14),
      'health.disable_below': String(healthDisableBelow.value() ?? 0),
      'health.show_users': String(healthShowUsers.value()),
      'health.warn_below': String(healthWarnBelow.value() ?? 0),
    };
  }

  /**
   * What the policy is holding right now, and a way to empty it immediately.
   *
   * The figure matters more than it looks: without it an operator has to
   * trust that their cleanup is working rather than watch it work. The button
   * is the same operation the schedule performs, so someone who has just
   * changed the policy — or been asked to delete something now — does not
   * have to wait until three in the morning to find out.
   */
  function heldPanel(initial: HeldAttachments): HTMLElement {
    const wrap = el('div', 'oa-field');
    const figure = el('p', 'oa-field-hint');

    function paint(held: number, bytes: number): void {
      figure.textContent = held
        ? t('attachmentsHeld', { count: held, size: megabytes(bytes) })
        : t('attachmentsHeldNone');
    }
    paint(initial.held, initial.bytes);

    const purge = confirmable(
      button('oa-btn', t('purgeNow'), () => {}),
      { label: t('purgeNowConfirm'), title: t('purgeNow') },
      () => {
        purge.disabled = true;
        void adminApi.purgeAttachments()
          .then((result) => {
            paint(result.attachments.held, result.attachments.bytes);
            flash.textContent = t('purgeDone', { count: result.purged });
            flash.classList.add('visible');
          })
          .catch((error: unknown) => {
            flash.textContent = error instanceof ApiError ? error.message : String(error);
            flash.classList.add('visible');
          })
          .finally(() => { purge.disabled = false; });
      },
    );

    wrap.appendChild(figure);
    wrap.appendChild(purge);
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

/** Bytes as a figure a person reads, to one decimal below a gigabyte. */
function megabytes(bytes: number): string {
  const mb = bytes / (1024 * 1024);
  if (mb >= 1024) return `${(mb / 1024).toFixed(1)} GB`;
  if (mb >= 10) return `${Math.round(mb)} MB`;
  if (mb >= 0.1) return `${mb.toFixed(1)} MB`;
  return `${Math.max(1, Math.round(bytes / 1024))} KB`;
}
