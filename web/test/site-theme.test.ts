import { afterEach, beforeEach, expect, it } from 'vitest';
import {
  accentPreference,
  backgroundAccent,
  modeLocked,
  ownWallpaper,
  pickBackground,
  setAccentPreference,
  setBackgroundAccent,
  setSignedIn,
  setSiteAppearance,
  setThemeMode,
  setWallpaper,
  startTheme,
  themeMode,
  wallpaper,
} from '../src/theme/theme';

const SITE = {
  mode: 'dark',
  accent: 'teal',
  custom_accent: '',
  background_accent: 'pink',
  dim: 40,
  blur: 6,
  translucency: 30,
  panel_blur: 12,
  enforce: false,
};

const BACKGROUNDS = {
  landscape_light: { url: '/api/site/login-background/landscape_light?v=1', html: false },
  app_landscape_dark: { url: '/api/site/login-background/app_landscape_dark?v=2', html: true },
  app_portrait_light: { url: '/api/site/login-background/app_portrait_light?v=3', html: false },
};

beforeEach(() => {
  localStorage.clear();
  startTheme();
});

afterEach(() => {
  setSignedIn(false);
  setSiteAppearance(null, {});
  localStorage.clear();
});

it('uses the instance theme as the default a reader has not overridden', () => {
  setSiteAppearance(SITE, BACKGROUNDS);
  expect(themeMode()).toBe('dark');
  expect(accentPreference().accent).toBe('teal');
  expect(backgroundAccent()).toBe('pink');
  // Cached for the inline script, so the next load paints dark first.
  expect(localStorage.getItem('obsidian-arc-effective-theme')).toBe('dark');

  setThemeMode('light');
  setAccentPreference({ accent: 'red', customAccent: '' });
  // An empty tint is the reader choosing none, not "never chose".
  setBackgroundAccent('');
  expect(themeMode()).toBe('light');
  expect(accentPreference().accent).toBe('red');
  expect(backgroundAccent()).toBe('');
  expect(modeLocked()).toBe(false);
});

it('takes the reader settings over when enforced', () => {
  setThemeMode('light');
  setAccentPreference({ accent: 'red', customAccent: '' });
  setWallpaper({ url: '/api/preferences/wallpaper?v=9', dim: 0, blur: 0, translucency: 0, panelBlur: 0 });
  setSiteAppearance({ ...SITE, enforce: true }, BACKGROUNDS);
  setSignedIn(true);

  expect(themeMode()).toBe('dark');
  expect(modeLocked()).toBe(true);
  expect(accentPreference().accent).toBe('teal');
  // The reader's own is kept, for the day the instance lets go, but not shown.
  expect(ownWallpaper()?.url).toBe('/api/preferences/wallpaper?v=9');
  expect(wallpaper()?.url).toBe(BACKGROUNDS.app_landscape_dark.url);

  // "Follow system" under enforcement leaves the scheme to the reader.
  setSiteAppearance({ ...SITE, mode: 'auto', enforce: true }, BACKGROUNDS);
  expect(modeLocked()).toBe(false);
  expect(themeMode()).toBe('light');
});

it('paints the signed-in background only for somebody signed in, and a page without a picture layer', () => {
  setSiteAppearance(SITE, BACKGROUNDS);
  expect(wallpaper()).toBeNull();

  setSignedIn(true);
  const painted = wallpaper();
  // Dark scheme, landscape screen: the exact variant, which is a page.
  expect(painted).toMatchObject({ url: BACKGROUNDS.app_landscape_dark.url, html: true, dim: 40, translucency: 30, panelBlur: 12 });
  const root = document.documentElement;
  expect(root.classList.contains('has-wallpaper')).toBe(true);
  expect(root.style.getPropertyValue('--ai-wallpaper')).toBe('');
  expect(root.style.getPropertyValue('--ai-surface-opacity')).toBe('70%');

  // Light has no landscape variant: the portrait one in the same brightness
  // wins over the landscape one in the other.
  setThemeMode('light');
  expect(wallpaper()).toMatchObject({ url: BACKGROUNDS.app_portrait_light.url, html: false });
  expect(root.style.getPropertyValue('--ai-wallpaper')).toContain('app_portrait_light');

  // The reader's own wallpaper wins when nothing is enforced.
  setWallpaper({ url: '/api/preferences/wallpaper?v=9', dim: 0, blur: 0, translucency: 0, panelBlur: 0 });
  expect(wallpaper()?.url).toBe('/api/preferences/wallpaper?v=9');

  setWallpaper(null);
  setSignedIn(false);
  expect(wallpaper()).toBeNull();
  expect(root.classList.contains('has-wallpaper')).toBe(false);
});

it('ignores a background URL that is not a path on this server', () => {
  setSiteAppearance(SITE, { app_landscape_dark: { url: 'https://elsewhere.example/x', html: true } });
  setSignedIn(true);
  expect(wallpaper()).toBeNull();
});

it('gives a tablet its own variant, and the nearest shape when it has none', () => {
  const bg = (name: string) => ({ url: `/api/site/login-background/${name}?v=1`, html: false });
  const set = { landscape_dark: bg('landscape_dark'), portrait_dark: bg('portrait_dark'), tablet_light: bg('tablet_light') };
  expect(pickBackground(set, '', 'tablet', false)?.url).toContain('tablet_light');
  // Dark has no tablet picture: the desktop one, before the phone's.
  expect(pickBackground(set, '', 'tablet', true)?.url).toContain('landscape_dark');
  // A desktop or a phone borrows the tablet's before the other's.
  expect(pickBackground({ tablet_dark: bg('tablet_dark'), portrait_dark: bg('portrait_dark') }, '', 'desktop', true)?.url).toContain('tablet_dark');
  expect(pickBackground({ tablet_dark: bg('tablet_dark'), landscape_dark: bg('landscape_dark') }, '', 'phone', true)?.url).toContain('tablet_dark');
  // Brightness before shape: a light desktop takes the light tablet over the dark desktop.
  expect(pickBackground(set, '', 'desktop', false)?.url).toContain('tablet_light');
});
