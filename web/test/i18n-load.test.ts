// Switching language while its dictionary is still on its way. The language
// the reader sees and the choice stored for the next visit change only once
// the dictionary has arrived, and a pick that a later pick overtook never
// takes effect.

import { afterEach, beforeEach, expect, it, vi } from 'vitest';

const KEY = 'obsidian-arc-language';

// Each test decides when the Chinese chunk arrives, or whether it does.
async function freshI18n(chunk: () => Promise<unknown>) {
  vi.resetModules();
  vi.doMock('../src/i18n.zh', chunk);
  return import('../src/i18n');
}

function gate(): { arrives: Promise<void>; release: () => void } {
  let release!: () => void;
  const arrives = new Promise<void>((resolve) => { release = resolve; });
  return { arrives, release };
}

beforeEach(() => {
  localStorage.clear();
});

afterEach(() => {
  vi.doUnmock('../src/i18n.zh');
  vi.resetModules();
  localStorage.clear();
});

it('keeps the language and the stored choice as they were until the dictionary arrives', async () => {
  const chunk = gate();
  const i18n = await freshI18n(async () => {
    await chunk.arrives;
    return { zh: { languageLabel: '语言' } };
  });

  const pick = i18n.setLanguage('zh');
  expect(i18n.language()).toBe('en');
  expect(localStorage.getItem(KEY)).toBeNull();

  chunk.release();
  await pick;
  expect(i18n.language()).toBe('zh');
  expect(i18n.t('languageLabel')).toBe('语言');
  expect(localStorage.getItem(KEY)).toBe('zh');
});

it('leaves the language and the stored choice alone when the dictionary does not arrive', async () => {
  const i18n = await freshI18n(async () => {
    throw new Error('chunk did not arrive');
  });

  await expect(i18n.setLanguage('zh')).rejects.toThrow();
  expect(i18n.language()).toBe('en');
  expect(localStorage.getItem(KEY)).toBeNull();
});

it('does not switch back to a language the reader has since moved away from', async () => {
  const chunk = gate();
  const i18n = await freshI18n(async () => {
    await chunk.arrives;
    return { zh: { languageLabel: '语言' } };
  });

  const first = i18n.setLanguage('zh');
  await i18n.setLanguage('en');
  chunk.release();
  await first;

  expect(i18n.language()).toBe('en');
  expect(localStorage.getItem(KEY)).toBe('en');
});

// The backoffice's English is not in the main bundle: it is added when the
// backoffice's chunk is evaluated, and the shell is what brings it in. The
// setup file adds it for every other test, so this one starts from a module
// graph without it to see that the shell alone is enough.
it('has the backoffice’s English once the backoffice shell is loaded, not before', async () => {
  vi.resetModules();
  const i18n = await import('../src/i18n');
  expect(i18n.t('navAudit')).toBe('navAudit');
  expect(i18n.t('newChat')).toBe('New chat');

  await import('../src/views/admin/AdminPage.vue');
  expect(i18n.t('navAudit')).toBe('Audit trail');
});

it('resolves quietly when the dictionary of an overtaken pick fails', async () => {
  const chunk = gate();
  const i18n = await freshI18n(async () => {
    await chunk.arrives;
    throw new Error('chunk did not arrive');
  });

  const first = i18n.setLanguage('zh');
  await i18n.setLanguage('en');
  chunk.release();

  await expect(first).resolves.toBeUndefined();
  expect(i18n.language()).toBe('en');
  expect(localStorage.getItem(KEY)).toBe('en');
});
