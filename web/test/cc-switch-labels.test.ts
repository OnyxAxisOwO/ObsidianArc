import { expect, it } from 'vitest';
import { zh } from '../src/i18n.zh';
import { changeLanguage, t, type StringKey } from '../src/composables/useI18n';

// Cyrillic letters that look like Latin ones (С, а, о, е) pass for ASCII on
// screen but break find-in-page, copy-paste and screen readers.
const LOOKALIKE = /[Ѐ-ӿ]/;

it('spells the CC Switch client labels with Latin letters in both languages', async () => {
  const keys = (Object.keys(zh) as StringKey[]).filter((key) => key.startsWith('keyCCSwitch'));
  expect(keys.length).toBeGreaterThan(5);
  for (const lang of ['en', 'zh'] as const) {
    await changeLanguage(lang);
    expect(t('keyCCSwitchClaude')).toBe('Claude Code');
    expect(t('keyCCSwitchCodex')).toBe('Codex');
    for (const key of keys) expect(t(key), `${lang} ${key}`).not.toMatch(LOOKALIKE);
  }
});
