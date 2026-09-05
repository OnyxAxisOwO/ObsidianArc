// User settings.
//
// The standalone build kept these in a right-side drawer, mixed in with the
// API key and the base URL. Those moved to administration, and what is left
// is what belongs to a person rather than to the server: who they are, what
// the interface looks like, and what a new conversation starts with.
//
// The accent picker is the same control it always was — ten dots and a custom
// hex, one hue clamped per scheme so the choice works against both a white
// and a near-black background. It is carried over rather than redesigned
// because it was already the best thing on the settings panel.

import { changePassword, updateProfile } from '../api/auth';
import { ApiError, api } from '../api/client';
import { renderShell } from '../app/shell';
import { t } from '../i18n';
import { adopt, currentPreferences, currentUser, persistTheme, syncPreferences } from '../session';
import { ACCENTS, ACCENT_NAMES, baseAccent, normalizeHex, type AccentName } from '../theme/color-utils';
import {
  accentPreference,
  setAccentPreference,
  setWallpaper,
  themeMode,
  wallpaper,
  type ThemeMode,
} from '../theme/theme';
import { prepareImage, ImageError } from '../chat/image';
import { button, el } from '../ui/dom';
import { numberField, selectField, textArea, textField } from '../ui/form';
import { language, setLanguage, type Language } from '../i18n';

export function renderSettingsPage(root: HTMLElement): void {
  const shell = renderShell(root);
  shell.body.classList.add('oa-admin');

  const account = currentUser();
  if (!account) return;

  const main = el('div', 'oa-admin-main');
  const head = el('div', 'oa-admin-head');
  const heading = el('div');
  heading.appendChild(el('h1', 'oa-admin-title', t('settings')));
  heading.appendChild(el('p', 'oa-admin-subtitle', `@${account.username}`));
  head.appendChild(heading);
  head.appendChild(el('span', 'oa-admin-head-spacer'));

  const body = el('div', 'oa-admin-body');
  main.appendChild(head);
  main.appendChild(body);
  shell.body.appendChild(main);

  const form = el('div', 'oa-settings');
  body.appendChild(form);

  form.appendChild(appearanceSection());
  form.appendChild(wallpaperSection());
  form.appendChild(chatSection());
  form.appendChild(profileSection());
  form.appendChild(passwordSection());
}

// --- appearance ---------------------------------------------------------------

function appearanceSection(): HTMLElement {
  const wrap = panel('Appearance');

  const theme = selectField<ThemeMode>({
    label: 'Theme',
    value: themeMode(),
    options: [
      { value: 'auto', label: 'Match the system' },
      { value: 'light', label: 'Light' },
      { value: 'dark', label: 'Dark' },
    ],
    onChange: (value) => {
      // Applied through the session, so the choice follows the account to
      // another device rather than living only in this browser.
      persistTheme(value);
      paintAccentGrid();
    },
  });
  wrap.appendChild(theme.element);

  wrap.appendChild(el('span', 'oa-field-label', 'Accent colour'));
  wrap.appendChild(el('span', 'oa-field-hint',
    'One hue, adjusted per scheme so it stays readable on both a white and a near-black background.'));

  const grid = el('div', 'oa-color-grid');
  const dots = ACCENT_NAMES.map((name) => {
    const dot = button('oa-color-dot', '', () => {
      applyAccent(name, accentPreference().customAccent);
    });
    dot.style.backgroundColor = ACCENTS[name];
    dot.title = accentLabel(name);
    dot.setAttribute('aria-label', dot.title);
    grid.appendChild(dot);
    return { name, dot };
  });
  wrap.appendChild(grid);

  const customRow = el('div', 'oa-color-custom-row oa-input-row');
  const swatch = el('input');
  swatch.type = 'color';
  const hex = el('input');
  hex.type = 'text';
  hex.placeholder = '#6C4CD6';
  hex.maxLength = 7;
  customRow.appendChild(swatch);
  customRow.appendChild(hex);
  wrap.appendChild(customRow);

  swatch.addEventListener('input', () => applyAccent('custom', swatch.value));
  hex.addEventListener('change', () => applyAccent('custom', hex.value));

  const languageField = selectField<Language>({
    label: 'Language',
    value: language(),
    options: [{ value: 'en', label: 'English' }, { value: 'zh', label: '中文' }],
    hint: 'Takes effect on the next screen you open.',
    onChange: (value) => {
      setLanguage(value);
      syncPreferences({ language: value });
    },
  });
  wrap.appendChild(languageField.element);

  function applyAccent(accent: AccentName | 'custom', custom: string): void {
    const normalized = accent === 'custom' ? normalizeHex(custom, null) : '';
    if (accent === 'custom' && !normalized) return;
    setAccentPreference({ accent, customAccent: normalized ?? '' });
    syncPreferences({ accent, custom_accent: normalized ?? '' });
    paintAccentGrid();
  }

  function paintAccentGrid(): void {
    const pref = accentPreference();
    const base = baseAccent(pref);
    const custom = pref.accent === 'custom';
    for (const entry of dots) entry.dot.classList.toggle('active', !custom && pref.accent === entry.name);
    customRow.classList.toggle('active', custom);
    // A dot click updates these two as well, so they read as "here is the hex
    // you just picked" rather than freezing on whatever was last typed.
    swatch.value = base;
    hex.value = base;
  }

  paintAccentGrid();
  return wrap;
}

function accentLabel(name: AccentName): string {
  const labels: Record<AccentName, string> = {
    violet: 'Violet', neutral: 'Black & white', red: 'Red', pink: 'Pink',
    indigo: 'Indigo', blue: 'Blue', cyan: 'Cyan', teal: 'Teal',
    green: 'Green', orange: 'Orange',
  };
  return labels[name];
}

// --- wallpaper -----------------------------------------------------------------

function wallpaperSection(): HTMLElement {
  const wrap = panel('Wallpaper', 'Sits behind the interface. Stored on your account, not in this browser.');

  const current = wallpaper();
  const status = el('p', 'oa-field-hint', current ? 'A wallpaper is set.' : 'No wallpaper.');

  const picker = el('input');
  picker.type = 'file';
  picker.accept = 'image/png,image/jpeg,image/webp,image/avif';
  picker.hidden = true;

  const choose = button('oa-btn', 'Choose an image…', () => picker.click());
  const remove = button('oa-btn oa-btn-danger', 'Remove', () => void clearWallpaper());
  remove.hidden = !current;

  const dim = numberField({
    label: 'Dim',
    value: current?.dim ?? 30,
    min: 0,
    max: 100,
    hint: 'How much to darken it, so interface text stays readable. 0–100.',
  });
  const blur = numberField({ label: 'Blur', value: current?.blur ?? 0, min: 0, max: 40 });

  const apply = button('oa-btn', 'Apply', () => {
    const existing = wallpaper();
    if (!existing) return;
    const next = { url: existing.url, dim: dim.value() ?? 0, blur: blur.value() ?? 0 };
    setWallpaper(next);
    syncPreferences({ wallpaper: next });
  });

  const row = el('div', 'oa-button-row');
  row.appendChild(choose);
  row.appendChild(apply);
  row.appendChild(remove);

  wrap.appendChild(status);
  wrap.appendChild(row);
  wrap.appendChild(dim.element);
  wrap.appendChild(blur.element);
  wrap.appendChild(picker);

  picker.addEventListener('change', () => {
    const file = picker.files?.[0];
    picker.value = '';
    if (file) void upload(file);
  });

  async function upload(file: File): Promise<void> {
    status.textContent = 'Preparing…';
    try {
      // The same downscale the composer uses. A phone photo as a wallpaper is
      // several megabytes of picture nobody will ever look at closely.
      const prepared = await prepareImage(file);
      URL.revokeObjectURL(prepared.previewURL);

      status.textContent = 'Uploading…';
      const { url } = await api.put<{ url: string }>('/api/preferences/wallpaper', {
        mime: prepared.mime,
        data: prepared.data,
      });

      const next = { url, dim: dim.value() ?? 30, blur: blur.value() ?? 0 };
      setWallpaper(next);
      syncPreferences({ wallpaper: next });
      status.textContent = 'A wallpaper is set.';
      remove.hidden = false;
    } catch (error) {
      status.textContent = error instanceof ImageError || error instanceof ApiError
        ? error.message
        : String(error);
    }
  }

  async function clearWallpaper(): Promise<void> {
    try {
      await api.delete('/api/preferences/wallpaper');
    } catch {
      // Even if the server call fails, taking it off screen is what was asked.
    }
    setWallpaper(null);
    syncPreferences({ wallpaper: null });
    status.textContent = 'No wallpaper.';
    remove.hidden = true;
  }

  return wrap;
}

// --- chat defaults ---------------------------------------------------------------

function chatSection(): HTMLElement {
  const wrap = panel('Chat', 'What a new conversation starts with.');
  const preferences = currentPreferences();

  const models = el('select');
  const placeholder = el('option', null, 'The first available model');
  placeholder.value = '';
  models.appendChild(placeholder);

  const field = el('label', 'oa-field');
  field.appendChild(el('span', 'oa-field-label', 'Default model'));
  field.appendChild(models);
  wrap.appendChild(field);

  void api.get<{ models: Array<{ id: string; display_name: string; provider_name: string }> }>('/api/models')
    .then(({ models: list }) => {
      for (const model of list) {
        const option = el('option', null, `${model.display_name} — ${model.provider_name}`);
        option.value = model.id;
        models.appendChild(option);
      }
      const stored = preferences['default_model_id'];
      if (typeof stored === 'string') models.value = stored;
    })
    .catch(() => {
      placeholder.textContent = 'Could not load the model list.';
    });

  models.addEventListener('change', () => {
    syncPreferences({ default_model_id: models.value });
  });

  const effort = selectField({
    label: 'Default thinking effort',
    value: (preferences['reasoning_effort'] as string) ?? 'medium',
    options: [
      { value: 'low', label: t('effortLow') },
      { value: 'medium', label: t('effortMedium') },
      { value: 'high', label: t('effortHigh') },
    ],
    hint: 'Used when you turn on extended thinking. The toggle itself lives in the composer.',
    onChange: (value) => syncPreferences({ reasoning_effort: value }),
  });
  wrap.appendChild(effort.element);

  return wrap;
}

// --- profile ---------------------------------------------------------------------

function profileSection(): HTMLElement {
  const account = currentUser()!;
  const wrap = panel('Profile');

  const nickname = textField({
    label: 'Nickname',
    value: account.nickname,
    placeholder: account.username,
    maxLength: 32,
    hint: 'Shown instead of your username.',
  });
  const email = textField({ label: 'Email', value: account.email, type: 'email' });
  const bio = textArea({ label: 'Bio', value: account.bio, rows: 3 });
  const avatar = textField({
    label: 'Avatar',
    value: account.avatar,
    placeholder: 'https://… or leave empty for your initials',
    hint: 'A same-origin path or an inline image.',
  });

  const flash = el('p', 'oa-drawer-flash');
  const save = button('oa-btn primary', 'Save', () => void submit());

  wrap.appendChild(nickname.element);
  wrap.appendChild(email.element);
  wrap.appendChild(bio.element);
  wrap.appendChild(avatar.element);
  wrap.appendChild(flash);
  wrap.appendChild(buttonRow(save));

  async function submit(): Promise<void> {
    save.disabled = true;
    flash.classList.remove('visible');
    try {
      const { user } = await updateProfile({
        nickname: nickname.value(),
        email: email.value(),
        bio: bio.value(),
        avatar: avatar.value(),
      });
      adopt(user, currentPreferences());
      save.textContent = 'Saved';
      window.setTimeout(() => { save.textContent = 'Save'; }, 1500);
    } catch (error) {
      flash.textContent = error instanceof ApiError ? error.message : String(error);
      flash.classList.add('visible');
    } finally {
      save.disabled = false;
    }
  }

  return wrap;
}

function passwordSection(): HTMLElement {
  const wrap = panel('Password', 'Changing it signs you out everywhere else.');

  const current = textField({ label: 'Current password', type: 'password' });
  const next = textField({ label: 'New password', type: 'password', hint: 'At least 8 characters.' });
  const flash = el('p', 'oa-drawer-flash');
  const save = button('oa-btn', 'Change password', () => void submit());

  wrap.appendChild(current.element);
  wrap.appendChild(next.element);
  wrap.appendChild(flash);
  wrap.appendChild(buttonRow(save));

  async function submit(): Promise<void> {
    if (!current.value() || !next.value()) {
      flash.textContent = 'Fill in both fields.';
      flash.classList.add('visible');
      return;
    }
    save.disabled = true;
    flash.classList.remove('visible');
    try {
      await changePassword(current.value(), next.value());
      current.set('');
      next.set('');
      save.textContent = 'Changed';
      window.setTimeout(() => { save.textContent = 'Change password'; }, 1500);
    } catch (error) {
      flash.textContent = error instanceof ApiError ? error.message : String(error);
      flash.classList.add('visible');
    } finally {
      save.disabled = false;
    }
  }

  return wrap;
}

// --- helpers -----------------------------------------------------------------------

function panel(title: string, hint?: string): HTMLElement {
  const wrap = el('div', 'oa-settings-panel');
  wrap.appendChild(el('h2', 'oa-admin-section-title', title));
  if (hint) wrap.appendChild(el('p', 'oa-field-hint', hint));
  return wrap;
}

function buttonRow(...nodes: HTMLElement[]): HTMLElement {
  const row = el('div', 'oa-button-row');
  for (const node of nodes) row.appendChild(node);
  return row;
}
