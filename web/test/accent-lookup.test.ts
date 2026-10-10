import { beforeEach, describe, expect, it, vi } from 'vitest';

import { ACCENTS, DEFAULT_ACCENT, baseAccent, type AccentName } from '../src/theme/color-utils';
import { accentPreference } from '../src/theme/theme';

// The stored accent is read back from the account on every session restore,
// so a name that an ordinary object answers for must not reach the palette.
// `in` and bracket access both answer for constructor and __proto__.

describe('a stored accent name that only the prototype has', () => {
  beforeEach(() => {
    localStorage.clear();
  });

  it.each(['constructor', '__proto__', 'toString', 'hasOwnProperty'])('%s falls back to the default accent', (name) => {
    localStorage.setItem('obsidian-arc-accent', JSON.stringify({ accent: name, customAccent: '' }));
    expect(accentPreference().accent).toBe(DEFAULT_ACCENT);
  });

  it('is never looked up as a colour', () => {
    expect(baseAccent({ accent: 'constructor' as AccentName, customAccent: '' })).toBe(ACCENTS[DEFAULT_ACCENT]);
    expect(baseAccent({ accent: '__proto__' as AccentName, customAccent: '' })).toBe(ACCENTS[DEFAULT_ACCENT]);
  });

  it('does not stop the next boot, which applies the stored accent before the first paint', async () => {
    localStorage.setItem('obsidian-arc-accent', JSON.stringify({ accent: 'constructor', customAccent: '' }));
    vi.resetModules();
    const theme = await import('../src/theme/theme');

    expect(() => theme.startTheme()).not.toThrow();
    const painted = document.documentElement.style.getPropertyValue('--ai-primary');
    expect(painted).not.toBe('');

    theme.setAccentPreference({ accent: DEFAULT_ACCENT, customAccent: '' });
    expect(document.documentElement.style.getPropertyValue('--ai-primary')).toBe(painted);
  });

  it('does not throw when the reader saves one, and the default is what applies', async () => {
    vi.resetModules();
    const theme = await import('../src/theme/theme');
    expect(() => theme.setAccentPreference({ accent: 'constructor' as AccentName, customAccent: '' })).not.toThrow();
    expect(theme.accentPreference().accent).toBe(DEFAULT_ACCENT);
  });
});
