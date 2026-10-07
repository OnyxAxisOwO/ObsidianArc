import { afterEach, beforeEach, expect, it, vi } from 'vitest';
import { createApp, nextTick, type App } from 'vue';
import AppearanceSection from '../src/views/settings/AppearanceSection.vue';
import { accentPreference, backgroundAccent, setAccentPreference, setBackgroundAccent, setThemeMode, setWallpaper, ownWallpaper as wallpaper } from '../src/theme/theme';
import { t } from '../src/composables/useI18n';
import * as auth from '../src/api/auth';
import { api } from '../src/api/client';
import { prepareImage } from '../src/chat/image';
import { adopt, currentPreferences, forget } from '../src/stores/session';

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
afterEach(() => {
  app?.unmount(); app = undefined; document.body.textContent = '';
  setWallpaper(null); localStorage.clear(); forget(); vi.restoreAllMocks(); vi.unstubAllGlobals();
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
