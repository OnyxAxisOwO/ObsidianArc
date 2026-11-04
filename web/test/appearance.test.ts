import { afterEach, beforeEach, expect, it, vi } from 'vitest';
import { createApp, nextTick, type App } from 'vue';
import AppearanceSection from '../src/views/settings/AppearanceSection.vue';
import ChatSection from '../src/views/settings/ChatSection.vue';
import { accentPreference, backgroundAccent, onThemeChange, setAccentPreference, setBackgroundAccent, setThemeMode, setWallpaper, ownWallpaper as wallpaper } from '../src/theme/theme';
import { changeLanguage, t } from '../src/composables/useI18n';
import * as auth from '../src/api/auth';
import { api } from '../src/api/client';
import { prepareImage } from '../src/chat/image';
import { adopt, currentPreferences, forget } from '../src/stores/session';
import { settle } from './helpers';

vi.mock('../src/chat/image', async (original) => ({
  ...await original<typeof import('../src/chat/image')>(), prepareImage: vi.fn(),
}));

const account: auth.Account = {
  id: 'member', username: 'member', email: '', nickname: '', avatar: '', bio: '', role: 'user',
  group_id: '', group_expires_at: 0, group_name: '', status: 'active', created_at: 0, updated_at: 0,
  last_login_at: 0, email_verified: true, allow_stats: true, allow_delete_conversations: true,
  api_restricted: false, api_restricted_until: 0, api_restriction_source: '',
};

let app: App | undefined;
beforeEach(() => {
  vi.spyOn(auth, 'savePreferences').mockResolvedValue({ preferences: {} });
  adopt(account);
});
afterEach(async () => {
  app?.unmount(); app = undefined; document.body.textContent = '';
  setWallpaper(null); localStorage.clear(); forget(); vi.restoreAllMocks(); vi.unstubAllGlobals();
  await changeLanguage('en');
});

it('keeps wallpaper, background and control colours independent, including a background reset', async () => {
  setThemeMode('light');
  setAccentPreference({ accent: 'blue', customAccent: '' });
  setBackgroundAccent('');
  const paper = { url: '/api/preferences/wallpaper?v=1', dim: 20, blur: 3, translucency: 40, panelBlur: 5 };
  adopt(account, { wallpaper: paper });
  const host = document.createElement('div'); document.body.appendChild(host);
  app = createApp(AppearanceSection); app.mount(host);
  const primary = document.documentElement.style.getPropertyValue('--ai-primary');
  const mint = [...host.querySelectorAll<HTMLButtonElement>('.oa-background-choice')].find((el) => el.textContent?.includes(t('backgroundMint')))!;
  mint.click(); await nextTick();
  expect(backgroundAccent()).toBe('teal');
  expect(wallpaper()).toEqual(paper);
  expect(currentPreferences.value.wallpaper).toEqual(paper);
  expect(auth.savePreferences).toHaveBeenLastCalledWith({ background_accent: 'teal' });
  expect(mint.getAttribute('aria-pressed')).toBe('true');
  expect(document.documentElement.style.getPropertyValue('--ai-primary')).toBe(primary);
  const surface = document.documentElement.style.getPropertyValue('--ai-field-bg');
  host.querySelector<HTMLButtonElement>(`.oa-color-dot[aria-label="${t('accentPink')}"]`)!.click();
  await nextTick();
  expect(accentPreference().accent).toBe('pink');
  expect(backgroundAccent()).toBe('teal');
  expect(document.documentElement.style.getPropertyValue('--ai-field-bg')).toBe(surface);
  expect(document.documentElement.style.getPropertyValue('--ai-primary')).not.toBe(primary);
  const cache = JSON.parse(localStorage.getItem('obsidian-arc-palette')!);
  expect(cache.light).toContain(surface);
  expect(host.querySelector('.oa-preview-composer')).toBeNull();
  const reset = [...host.querySelectorAll<HTMLButtonElement>('.oa-appearance-heading button')].find((el) => el.textContent === t('reset'))!;
  reset.click(); await nextTick();
  expect(backgroundAccent()).toBe('');
  expect(mint.getAttribute('aria-pressed')).toBe('false');
  expect(wallpaper()).toEqual(paper);
  expect(auth.savePreferences).toHaveBeenLastCalledWith({ background_accent: '' });
});

it('preserves the selected background through wallpaper upload, session restore and removal', async () => {
  adopt(account, { background_accent: 'teal', wallpaper: null });
  vi.stubGlobal('URL', class extends URL { static override revokeObjectURL = vi.fn(); });
  vi.mocked(prepareImage).mockResolvedValue({ mime: 'image/jpeg', data: 'AA==', width: 1, height: 1, bytes: 1, previewURL: 'blob:preview' });
  const url = '/api/preferences/wallpaper?v=2';
  const upload = vi.spyOn(api, 'put').mockResolvedValue({ url });
  const remove = vi.spyOn(api, 'delete').mockResolvedValue(undefined);
  const host = document.createElement('div'); document.body.appendChild(host);
  app = createApp(AppearanceSection); app.mount(host);
  const mint = [...host.querySelectorAll<HTMLButtonElement>('.oa-background-choice')].find((el) => el.textContent?.includes(t('backgroundMint')))!;
  const surface = document.documentElement.style.getPropertyValue('--ai-field-bg');
  const picker = host.querySelector<HTMLInputElement>('input[type="file"]')!;
  Object.defineProperty(picker, 'files', { value: [new File(['image'], 'wallpaper.png', { type: 'image/png' })] });
  picker.dispatchEvent(new Event('change', { bubbles: true }));
  await new Promise((resolve) => setTimeout(resolve, 0)); await nextTick();
  expect(upload).toHaveBeenCalledWith('/api/preferences/wallpaper', { mime: 'image/jpeg', data: 'AA==' });
  expect(wallpaper()?.url).toBe(url);
  expect(backgroundAccent()).toBe('teal');
  expect(mint.getAttribute('aria-pressed')).toBe('true');
  expect(mint.querySelector('.oa-background-check')).not.toBeNull();
  expect(document.documentElement.style.getPropertyValue('--ai-field-bg')).toBe(surface);
  expect(document.documentElement.classList.contains('has-wallpaper')).toBe(true);
  expect(auth.savePreferences).toHaveBeenLastCalledWith({ wallpaper: wallpaper() });

  const saved = { ...currentPreferences.value };
  localStorage.clear(); adopt(account, saved); await nextTick();
  expect(backgroundAccent()).toBe('teal');
  expect(wallpaper()?.url).toBe(url);
  expect(mint.getAttribute('aria-pressed')).toBe('true');
  host.querySelector<HTMLButtonElement>('.oa-wallpaper-current button')!.click();
  await new Promise((resolve) => setTimeout(resolve, 0)); await nextTick();
  expect(remove).toHaveBeenCalledWith('/api/preferences/wallpaper');
  expect(wallpaper()).toBeNull();
  expect(backgroundAccent()).toBe('teal');
  expect(mint.getAttribute('aria-pressed')).toBe('true');
  expect(currentPreferences.value.background_accent).toBe('teal');
  expect(auth.savePreferences).toHaveBeenLastCalledWith({ wallpaper: null });
});

it('sends the custom colour once, when the picker closes, not on every step of the drag', async () => {
  const host = document.createElement('div'); document.body.appendChild(host);
  app = createApp(AppearanceSection); app.mount(host);
  const picker = host.querySelector<HTMLInputElement>('input[type="color"]')!;
  for (const hex of ['#100000', '#400000', '#700000', '#a00000']) {
    picker.value = hex;
    picker.dispatchEvent(new Event('input', { bubbles: true }));
  }
  await nextTick();
  expect(auth.savePreferences).not.toHaveBeenCalled();
  expect(accentPreference().accent).toBe('custom');
  expect(accentPreference().customAccent).toBe('#A00000');

  picker.value = '#a00000';
  picker.dispatchEvent(new Event('change', { bubbles: true }));
  await nextTick();
  expect(auth.savePreferences).toHaveBeenCalledTimes(1);
  expect(auth.savePreferences).toHaveBeenCalledWith({ accent: 'custom', custom_accent: '#A00000' });
});

it('moves a wallpaper slider without storing or announcing each step, and stores once on release', async () => {
  const paper = { url: '/api/preferences/wallpaper?v=3', dim: 20, blur: 3, translucency: 40, panelBlur: 5 };
  setWallpaper(paper);
  const listener = vi.fn();
  const stop = onThemeChange(listener);
  const stored = () => vi.mocked(Storage.prototype.setItem).mock.calls.filter(([key]) => key === 'obsidian-arc-wallpaper');
  vi.spyOn(Storage.prototype, 'setItem');
  const host = document.createElement('div'); document.body.appendChild(host);
  app = createApp(AppearanceSection); app.mount(host);
  await nextTick();
  listener.mockClear();
  vi.mocked(Storage.prototype.setItem).mockClear();

  const dim = host.querySelector<HTMLInputElement>('input[type="range"]')!;
  for (const value of [30, 40, 50]) {
    dim.value = String(value);
    dim.dispatchEvent(new Event('input', { bubbles: true }));
  }
  await nextTick();
  expect(document.documentElement.style.getPropertyValue('--ai-wallpaper-dim')).toBe('0.5');
  expect(stored()).toHaveLength(0);
  expect(listener).not.toHaveBeenCalled();
  expect(auth.savePreferences).not.toHaveBeenCalled();

  dim.dispatchEvent(new Event('change', { bubbles: true }));
  await nextTick();
  expect(stored()).toHaveLength(1);
  expect(JSON.parse(stored()[0]![1])).toMatchObject({ dim: 50 });
  expect(listener).toHaveBeenCalledTimes(1);
  expect(auth.savePreferences).toHaveBeenCalledTimes(1);
  expect(auth.savePreferences).toHaveBeenCalledWith({ wallpaper: expect.objectContaining({ dim: 50 }) });
  stop();
});

it('paints the wallpaper with no filter at zero blur, and with one blur otherwise', () => {
  const style = document.documentElement.style;
  const paper = { url: '/api/preferences/wallpaper?v=4', dim: 20, translucency: 0, panelBlur: 0 };

  setWallpaper({ ...paper, blur: 0 });
  // The stylesheet reads this for the wallpaper layers. blur(0px) would still
  // promote a full-viewport layer, so the no-blur case has to be `none`.
  expect(style.getPropertyValue('--ai-wallpaper-filter')).toBe('none');
  setWallpaper({ ...paper, blur: 3 });
  expect(style.getPropertyValue('--ai-wallpaper-filter')).toBe('blur(3px)');
  setWallpaper(null);
  expect(style.getPropertyValue('--ai-wallpaper-filter')).toBe('');
});

it('relabels the default-model choices when the language changes under a panel that stays mounted', async () => {
  vi.spyOn(api, 'get').mockResolvedValue({ models: [{ id: 'm1', display_name: 'Model One' }] });
  const host = document.createElement('div'); document.body.appendChild(host);
  app = createApp(ChatSection); app.mount(host);
  await settle();
  expect(host.textContent).toContain(t('firstAvailableModel'));

  await changeLanguage('zh');
  await settle();
  expect(t('firstAvailableModel')).not.toBe('The first available model');
  expect(host.textContent).toContain(t('firstAvailableModel'));
  expect(host.textContent).not.toContain('The first available model');
});
