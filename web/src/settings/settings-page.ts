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
import { renderChatPage } from '../chat/chat-page';
import { navigate } from '../router';
import { openPanel } from '../ui/panel';
import { t, type StringKey } from '../i18n';
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
import { ICONS, button, clear, el, icon, iconButton } from '../ui/dom';
import { numberField, selectField, textArea, textField } from '../ui/form';
import { language, setLanguage, type Language } from '../i18n';

// Five sections is more than fits a panel without scrolling past what you
// came for, so they are grouped into three and shown one group at a time.
// Which group is a menu rather than a row of tabs: the panel head has room
// for one more control, not for three labels plus the two buttons beside it.
type Category = 'appearance' | 'chat' | 'account';

const CATEGORIES: Array<{ id: Category; label: StringKey }> = [
  { id: 'appearance', label: 'secAppearance' },
  { id: 'chat', label: 'secChat' },
  { id: 'account', label: 'account' },
];

/**
 * Settings is the chat with a panel over it, not a page of its own.
 *
 * As a full page it was a dead end — nothing on it led back, because there
 * was nothing to lead back to that the route had not already replaced. As the
 * same column the admin screens edit rows in, it closes: by its own button,
 * by Escape, or by clicking away to the conversation still sitting behind it.
 *
 * There is no footer. Each section commits on its own — the theme the moment
 * it is picked, the profile and the password on their own buttons — so one
 * Save at the bottom would be claiming to commit things it has nothing to do
 * with.
 */
export function renderSettingsPage(root: HTMLElement): void {
  const host = renderChatPage(root);

  const account = currentUser();
  if (!account) return;

  let category: Category = 'appearance';
  let switchDirection: 'forward' | 'back' | 'rise' = 'rise';

  // Handles for in-place tab switches so rail/tabs don't flicker or re-mount
  let activeRailButtons: Map<Category, HTMLElement> | null = null;
  let activeTabButtons: Map<Category, HTMLElement> | null = null;
  let activeForm: HTMLElement | null = null;

  function buildForm(dir: 'forward' | 'back' | 'rise'): HTMLElement {
    const animClass = dir === 'forward'
      ? 'enter-forward'
      : dir === 'back'
        ? 'enter-back'
        : 'enter-rise';

    const form = el('div', `oa-settings ${animClass}`);
    form.appendChild(el('p', 'oa-settings-account', `@${account!.username}`));

    if (category === 'appearance') {
      form.appendChild(appearanceSection());
      form.appendChild(wallpaperSection());
    } else if (category === 'chat') {
      form.appendChild(chatSection());
    } else {
      form.appendChild(profileSection());
      form.appendChild(passwordSection());
    }
    return form;
  }

  function selectCategory(next: Category): void {
    if (next === category) return;
    const from = CATEGORIES.findIndex((e) => e.id === category);
    const to = CATEGORIES.findIndex((e) => e.id === next);
    const dir = to > from ? 'forward' : 'back';
    category = next;

    // Fast-path in-place update: keep tabs/rail mounted, smoothly toggle
    // .active and slide in the new form with PageDye directional animation.
    if (activeTabButtons && activeForm && activeForm.parentElement) {
      for (const [id, btn] of activeTabButtons) {
        btn.classList.toggle('active', id === category);
      }
      const newForm = buildForm(dir);
      activeForm.parentElement.replaceChild(newForm, activeForm);
      activeForm = newForm;
      panel.body.scrollTop = 0;
      return;
    }

    if (activeRailButtons && activeForm && activeForm.parentElement) {
      for (const [id, btn] of activeRailButtons) {
        btn.classList.toggle('active', id === category);
      }
      const newForm = buildForm(dir);
      activeForm.parentElement.replaceChild(newForm, activeForm);
      activeForm = newForm;
      panel.body.scrollTop = 0;
      return;
    }

    switchDirection = dir;
    panel.rebuild();
  }

  const fullscreen = iconButton('oa-icon-btn', ICONS.expand, t('fullscreen'), () => {
    const on = panel.toggleFullscreen();
    clear(fullscreen);
    fullscreen.appendChild(icon(on ? ICONS.collapse : ICONS.expand, 16));
    fullscreen.title = t(on ? 'exitFullscreen' : 'fullscreen');
    fullscreen.setAttribute('aria-label', fullscreen.title);
    switchDirection = 'rise';
    panel.rebuild();
  }, 16);

  const panel = openPanel({
    host,
    title: t('settings'),
    footer: false,
    width: 460,
    actions: [fullscreen],
    build: (body, handle) => {
      const form = buildForm(switchDirection);
      switchDirection = 'rise';
      activeForm = form;

      if (!handle.isFullscreen()) {
        activeRailButtons = null;

        // Drawer view: prominent segmented tab control right at the top,
        // matching the multi-pane navigation pattern from PageDye.
        const tabs = el('nav', 'oa-settings-tabs');
        const tabButtons = new Map<Category, HTMLElement>();
        for (const entry of CATEGORIES) {
          const btn = button(
            `oa-settings-tab${entry.id === category ? ' active' : ''}`,
            t(entry.label),
            () => selectCategory(entry.id),
          );
          tabs.appendChild(btn);
          tabButtons.set(entry.id, btn);
        }
        activeTabButtons = tabButtons;

        body.appendChild(tabs);
        body.appendChild(form);
        return;
      }

      activeTabButtons = null;

      const split = el('div', 'oa-settings-split');
      const rail = el('nav', 'oa-settings-rail');
      const railButtons = new Map<Category, HTMLElement>();
      for (const entry of CATEGORIES) {
        const btn = button(
          `oa-settings-rail-item${entry.id === category ? ' active' : ''}`,
          t(entry.label),
          () => selectCategory(entry.id),
        );
        rail.appendChild(btn);
        railButtons.set(entry.id, btn);
      }
      activeRailButtons = railButtons;

      split.appendChild(rail);
      split.appendChild(form);
      body.appendChild(split);
    },
    // Dismissing it should leave the URL on the conversation that is now in
    // front, but only if the panel is what the URL is still describing: a
    // click through to administration changes the route first, and following
    // that with a navigate would send the user back out of it.
    onClose: () => {
      if (window.location.pathname === '/settings') navigate('/', { replace: true });
    },
  });
}

// --- appearance ---------------------------------------------------------------

function appearanceSection(): HTMLElement {
  const wrap = panel(t('secAppearance'));

  const theme = selectField<ThemeMode>({
    label: t('theme'),
    value: themeMode(),
    options: [
      { value: 'auto', label: t('themeAuto') },
      { value: 'light', label: t('themeLight') },
      { value: 'dark', label: t('themeDark') },
    ],
    onChange: (value) => {
      // Applied through the session, so the choice follows the account to
      // another device rather than living only in this browser.
      persistTheme(value);
      paintAccentGrid();
    },
  });
  wrap.appendChild(theme.element);

  wrap.appendChild(el('span', 'oa-field-label', t('accentColour')));
  wrap.appendChild(el('span', 'oa-field-hint', t('accentHint')));

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
    label: t('languageLabel'),
    value: language(),
    options: [{ value: 'en', label: 'English' }, { value: 'zh', label: '中文' }],
    hint: t('languageHint'),
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
  const labels: Record<AccentName, StringKey> = {
    violet: 'accentViolet', neutral: 'accentNeutral', red: 'accentRed', pink: 'accentPink',
    indigo: 'accentIndigo', blue: 'accentBlue', cyan: 'accentCyan', teal: 'accentTeal',
    green: 'accentGreen', orange: 'accentOrange',
  };
  return t(labels[name]);
}

// --- wallpaper -----------------------------------------------------------------

function wallpaperSection(): HTMLElement {
  const wrap = panel(t('secWallpaper'), t('wallpaperHint'));

  const current = wallpaper();
  const status = el('p', 'oa-field-hint', current ? t('wallpaperSet') : t('wallpaperNone'));

  const picker = el('input');
  picker.type = 'file';
  picker.accept = 'image/png,image/jpeg,image/webp,image/avif';
  picker.hidden = true;

  const choose = button('oa-btn', t('chooseImage'), () => picker.click());
  const remove = button('oa-btn oa-btn-danger', t('remove'), () => void clearWallpaper());
  remove.hidden = !current;

  const dim = numberField({
    label: t('dim'),
    value: current?.dim ?? 30,
    min: 0,
    max: 100,
    hint: t('dimHint'),
  });
  const blur = numberField({ label: t('blur'), value: current?.blur ?? 0, min: 0, max: 40 });

  const apply = button('oa-btn', t('apply'), () => {
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
    status.textContent = t('preparing');
    try {
      // The same downscale the composer uses. A phone photo as a wallpaper is
      // several megabytes of picture nobody will ever look at closely.
      const prepared = await prepareImage(file);
      URL.revokeObjectURL(prepared.previewURL);

      status.textContent = t('uploading');
      const { url } = await api.put<{ url: string }>('/api/preferences/wallpaper', {
        mime: prepared.mime,
        data: prepared.data,
      });

      const next = { url, dim: dim.value() ?? 30, blur: blur.value() ?? 0 };
      setWallpaper(next);
      syncPreferences({ wallpaper: next });
      status.textContent = t('wallpaperSet');
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
    status.textContent = t('wallpaperNone');
    remove.hidden = true;
  }

  return wrap;
}

// --- chat defaults ---------------------------------------------------------------

function chatSection(): HTMLElement {
  const wrap = panel(t('secChatDefaults'), t('chatDefaultsHint'));
  const preferences = currentPreferences();

  const models = el('select');
  const placeholder = el('option', null, t('firstAvailableModel'));
  placeholder.value = '';
  models.appendChild(placeholder);

  const field = el('label', 'oa-field');
  field.appendChild(el('span', 'oa-field-label', t('defaultModel')));
  field.appendChild(models);
  wrap.appendChild(field);

  void api.get<{ models: Array<{ id: string; display_name: string; provider_name: string; usable?: boolean }> }>('/api/models')
    .then(({ models: list }) => {
      for (const model of list) {
        if (model.usable === false) continue;
        const option = el('option', null, `${model.display_name} — ${model.provider_name}`);
        option.value = model.id;
        models.appendChild(option);
      }
      const stored = preferences['default_model_id'];
      if (typeof stored === 'string') models.value = stored;
    })
    .catch(() => {
      placeholder.textContent = t('couldNotLoadModels');
    });

  models.addEventListener('change', () => {
    syncPreferences({ default_model_id: models.value });
  });

  const effort = selectField({
    label: t('defaultEffort'),
    value: (preferences['reasoning_effort'] as string) ?? 'medium',
    options: [
      { value: 'low', label: t('effortLow') },
      { value: 'medium', label: t('effortMedium') },
      { value: 'high', label: t('effortHigh') },
    ],
    hint: t('defaultEffortHint'),
    onChange: (value) => syncPreferences({ reasoning_effort: value }),
  });
  wrap.appendChild(effort.element);

  return wrap;
}

// --- profile ---------------------------------------------------------------------

function profileSection(): HTMLElement {
  const account = currentUser()!;
  const wrap = panel(t('secProfile'));

  const nickname = textField({
    label: t('nickname'),
    value: account.nickname,
    placeholder: account.username,
    maxLength: 32,
    hint: t('nicknameHint'),
  });
  const email = textField({ label: t('email'), value: account.email, type: 'email' });
  const bio = textArea({ label: t('bio'), value: account.bio, rows: 3 });
  const avatar = textField({
    label: t('avatar'),
    value: account.avatar,
    placeholder: t('avatarPlaceholderUser'),
    hint: t('avatarHint'),
  });

  const flash = el('p', 'oa-drawer-flash');
  const save = button('oa-btn primary', t('save'), () => void submit());

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
      save.textContent = t('saved');
      window.setTimeout(() => { save.textContent = t('save'); }, 1500);
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
  const wrap = panel(t('secPassword'), t('passwordSectionHint'));

  const current = textField({ label: t('currentPassword'), type: 'password' });
  const next = textField({ label: t('newPassword'), type: 'password', hint: t('newPasswordHint') });
  const flash = el('p', 'oa-drawer-flash');
  const save = button('oa-btn', t('changePassword'), () => void submit());

  wrap.appendChild(current.element);
  wrap.appendChild(next.element);
  wrap.appendChild(flash);
  wrap.appendChild(buttonRow(save));

  async function submit(): Promise<void> {
    if (!current.value() || !next.value()) {
      flash.textContent = t('fillBothFields');
      flash.classList.add('visible');
      return;
    }
    save.disabled = true;
    flash.classList.remove('visible');
    try {
      await changePassword(current.value(), next.value());
      current.set('');
      next.set('');
      save.textContent = t('changed');
      window.setTimeout(() => { save.textContent = t('changePassword'); }, 1500);
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
