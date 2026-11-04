// The backoffice's Chinese is a chunk of its own (src/i18n.admin.zh.ts). It
// must be there before any backoffice screen draws in Chinese — whether the
// backoffice is opened, reloaded or switched to Chinese from inside — and it
// must never be fetched for a reader in English. Every test starts from a
// fresh module graph, as a page load does, because the language and the
// dictionaries are read once per load.

import { afterEach, beforeEach, expect, it, vi } from 'vitest';

const KEY = 'obsidian-arc-language';

function gate(): { arrives: Promise<void>; release: () => void } {
  let release!: () => void;
  const arrives = new Promise<void>((resolve) => { release = resolve; });
  return { arrives, release };
}

// Counts the fetches of the backoffice's Chinese, and lets a test hold one.
function mockBackofficeChinese(hold?: Promise<void>, fail = false) {
  const fetched = vi.fn();
  vi.doMock('../src/i18n.admin.zh', async () => {
    fetched();
    await hold;
    if (fail) throw new Error('chunk did not arrive');
    return vi.importActual('../src/i18n.admin.zh');
  });
  return fetched;
}

// What the router does when /admin/... is opened: the route's own loader,
// called the way vue-router calls it before the page resolves.
async function openBackoffice(): Promise<void> {
  const { router } = await import('../src/router');
  const record = router.resolve('/admin/audit').matched.at(-1)!;
  const load = record.components!['default'] as () => Promise<unknown>;
  await load();
}

// Mounts one element that reads a key, and hands back its text as it stands.
// Vue is imported after the reset, with the dictionary it is to redraw from.
async function draw(text: () => string): Promise<{ text: () => string; settle: () => Promise<void> }> {
  const { createApp, h, nextTick } = await import('vue');
  const host = document.createElement('div');
  document.body.append(host);
  const app = createApp({ render: () => h('h1', text()) });
  app.mount(host);
  mounted.push(() => { app.unmount(); host.remove(); });
  return { text: () => host.textContent ?? '', settle: () => nextTick() };
}
const mounted: Array<() => void> = [];

beforeEach(() => {
  localStorage.clear();
  vi.resetModules();
});

afterEach(() => {
  for (const undo of mounted.splice(0)) undo();
  vi.doUnmock('../src/i18n.admin.zh');
  vi.doUnmock('../src/i18n.zh');
  vi.restoreAllMocks();
  vi.resetModules();
  localStorage.clear();
});

it('has the backoffice’s Chinese before the page resolves when /admin is opened in Chinese', async () => {
  localStorage.setItem(KEY, 'zh');
  const fetched = mockBackofficeChinese();
  const { primeLanguage, t } = await import('../src/composables/useI18n');
  await primeLanguage();
  // The core dictionary alone, as the chat needs it.
  expect(fetched).not.toHaveBeenCalled();
  expect(t('newChat')).toBe('新对话');

  await openBackoffice();
  expect(fetched).toHaveBeenCalledTimes(1);
  expect(t('navAudit')).toBe('审计日志');
  expect(t('auditSubtitle')).toBe('管理员在后台做过的每一件事，以及每一次查看他人对话。');

  // And the first render of a screen reading it is in Chinese.
  expect((await draw(() => t('navAudit'))).text()).toBe('审计日志');

  // Leaving and coming back fetches nothing again.
  await openBackoffice();
  expect(fetched).toHaveBeenCalledTimes(1);
});

it('waits for the backoffice’s Chinese while it is still on its way', async () => {
  localStorage.setItem(KEY, 'zh');
  const chunk = gate();
  mockBackofficeChinese(chunk.arrives);
  const { primeLanguage, t } = await import('../src/composables/useI18n');
  await primeLanguage();

  let opened = false;
  const opening = openBackoffice().then(() => { opened = true; });
  await new Promise((resolve) => setTimeout(resolve, 10));
  expect(opened).toBe(false);

  chunk.release();
  await opening;
  expect(t('navAudit')).toBe('审计日志');
});

it('never fetches the backoffice’s Chinese for a reader in English', async () => {
  localStorage.setItem(KEY, 'en');
  const fetched = mockBackofficeChinese();
  const { primeLanguage, t } = await import('../src/composables/useI18n');
  await primeLanguage();
  await openBackoffice();

  expect(fetched).not.toHaveBeenCalled();
  expect(t('navAudit')).toBe('Audit trail');
});

it('switches the backoffice to Chinese only once its half has arrived', async () => {
  localStorage.setItem(KEY, 'en');
  const chunk = gate();
  const fetched = mockBackofficeChinese(chunk.arrives);
  const { changeLanguage, currentLanguage, primeLanguage, t } = await import('../src/composables/useI18n');
  await primeLanguage();
  await openBackoffice();
  expect(fetched).not.toHaveBeenCalled();

  const screen = await draw(() => t('navAudit'));
  expect(screen.text()).toBe('Audit trail');

  const switching = changeLanguage('zh');
  await new Promise((resolve) => setTimeout(resolve, 10));
  // The core dictionary has landed by now, but the switch is still waiting:
  // nothing on screen has changed language yet.
  expect(fetched).toHaveBeenCalledTimes(1);
  expect(currentLanguage()).toBe('en');
  expect(screen.text()).toBe('Audit trail');

  chunk.release();
  await switching;
  await screen.settle();
  expect(currentLanguage()).toBe('zh');
  expect(screen.text()).toBe('审计日志');
});

it('opens the backoffice in English, never on key names, when its Chinese does not arrive', async () => {
  localStorage.setItem(KEY, 'zh');
  mockBackofficeChinese(undefined, true);
  vi.spyOn(console, 'warn').mockImplementation(() => {});
  const { primeLanguage, t } = await import('../src/composables/useI18n');
  await primeLanguage();

  await expect(openBackoffice()).resolves.toBeUndefined();
  expect(t('navAudit')).toBe('Audit trail');
  // The chat's own words are still in Chinese.
  expect(t('newChat')).toBe('新对话');
});

// A switch already on its way when the backoffice came into play — the
// account's stored language, applied as the session loads — is what the page
// waits on, so it has to bring the backoffice's half too.
it('brings the backoffice’s half into a switch that started before the backoffice did', async () => {
  const core = gate();
  vi.doMock('../src/i18n.zh', async () => {
    await core.arrives;
    return vi.importActual('../src/i18n.zh');
  });
  const fetched = mockBackofficeChinese();
  const i18n = await import('../src/i18n');

  const switching = i18n.setLanguage('zh');
  i18n.wantBackofficeLanguage();
  core.release();
  await switching;

  expect(fetched).toHaveBeenCalledTimes(1);
  expect(i18n.language()).toBe('zh');
  expect(i18n.t('navAudit')).toBe('审计日志');
});
