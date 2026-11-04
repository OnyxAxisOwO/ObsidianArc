import { afterEach, describe, expect, it, vi } from 'vitest';

// The class fades every colour at once. A page that loads with it would fade
// from the colours the pre-paint script set into the stored theme, so only a
// change the reader makes while the page is up may add it.
describe('the theme-switching class', () => {
  afterEach(() => {
    document.documentElement.classList.remove('theme-switching');
    vi.resetModules();
  });

  it('is not added by the apply that runs at startup', async () => {
    const theme = await import('../src/theme/theme');
    theme.startTheme();
    expect(document.documentElement.classList.contains('theme-switching')).toBe(false);
  });

  it('is added when the reader changes the theme after startup', async () => {
    const theme = await import('../src/theme/theme');
    theme.startTheme();
    theme.setThemeMode('dark');
    expect(document.documentElement.classList.contains('theme-switching')).toBe(true);
  });
});
