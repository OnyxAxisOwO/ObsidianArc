import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import { createApp, h, nextTick, ref, type App } from 'vue';
import fs from 'node:fs';
import path from 'node:path';
import { createMemoryHistory, createRouter } from 'vue-router';
import TerminalPanel from '../src/views/TerminalPanel.vue';
import { providePanelHost } from '../src/composables/usePanelHost';
import { changeLanguage, t } from '../src/composables/useI18n';
import type { ConsoleExecHandlers, ConsoleSpec } from '../src/api/console';
import { DEFAULT_TERMINAL_FONT_FAMILY, FONT_FAMILY_MAX_LENGTH } from '../src/terminal/prefs';

// `TerminalPanel.vue` and `session.ts` both talk to the server through
// `api/console.ts` alone, so mocking that one module is enough to drive the
// whole page without a network stub — the shape `terminal-plumbing.test.ts`
// (the other half of this feature) already exercises the real client
// against.
vi.mock('../src/api/console', () => ({
  fetchConsoleSpec: vi.fn(),
  completeConsoleLine: vi.fn(),
  execConsoleLine: vi.fn(),
}));

import { completeConsoleLine, execConsoleLine, fetchConsoleSpec } from '../src/api/console';

const BANNER = 'Obsidian Arc console — vtest';

function makeSpec(overrides: Partial<ConsoleSpec> = {}): ConsoleSpec {
  return {
    version: 'vtest',
    you: { username: 'ada', role: 'super_admin', permissions: ['users'] },
    ssh: { enabled: true, addr: ':2222', fingerprint: 'SHA256:test' },
    banner: BANNER,
    commands: [],
    ...overrides,
  };
}

/** Answers every `run()` with one output chunk and a successful `done`. */
function mockExec(outputText: string): void {
  vi.mocked(execConsoleLine).mockImplementation(async (_request, handlers: ConsoleExecHandlers) => {
    handlers.onOut?.(outputText);
    handlers.onDone?.({ ok: true, code: '', exit: false, elapsed_ms: 1, json: false, lang: 'en' });
  });
}

let app: App | undefined;
let host: HTMLElement;

async function settle(): Promise<void> {
  await new Promise((resolve) => setTimeout(resolve, 0));
  await nextTick();
}

async function mountTerminal(): Promise<void> {
  app = createApp({
    setup() {
      // AppShell provides the row a side panel becomes a column of; the
      // terminal is one, and its appearance settings are a second. A plain
      // element rather than a template ref, set once and never cleared, for
      // the reason AGENTS.md gives about refs during teardown.
      const row = document.createElement('div');
      host.appendChild(row);
      providePanelHost(ref(row));
      return () => h(TerminalPanel);
    },
  });
  // Closing the panel navigates back to the chat.
  app.use(createRouter({ history: createMemoryHistory(), routes: [{ path: '/:p(.*)*', component: { render: () => null } }] }));
  app.mount(host);
  await settle();
  await settle();
}

function input(node: HTMLInputElement | HTMLTextAreaElement, value: string): void {
  node.value = value;
  node.dispatchEvent(new Event('input', { bubbles: true }));
}

function key(node: Element, keyName: string): void {
  node.dispatchEvent(new KeyboardEvent('keydown', { key: keyName, bubbles: true, cancelable: true }));
}

beforeEach(async () => {
  await changeLanguage('en');
  vi.mocked(fetchConsoleSpec).mockResolvedValue(makeSpec());
  vi.mocked(completeConsoleLine).mockResolvedValue({ from: 0, items: [] });
  mockExec('42 users');
  host = document.createElement('div');
  document.body.appendChild(host);
  try {
    localStorage.clear();
  } catch {
    // A private window throws on clear(); nothing to do about it here either.
  }
});

afterEach(() => {
  app?.unmount();
  app = undefined;
  document.body.textContent = '';
  vi.mocked(fetchConsoleSpec).mockReset();
  vi.mocked(completeConsoleLine).mockReset();
  vi.mocked(execConsoleLine).mockReset();
  try {
    localStorage.clear();
  } catch {
    // See beforeEach.
  }
});

describe('the admin terminal', () => {
  // The rule that makes the terminal take the chat's whole width is keyed on
  // this class. OaPanel's root is a Teleport, which used to drop a caller's
  // class without a word, so the rule matched nothing and the terminal sat at
  // 720px beside an empty half of the screen.
  it('carries its own class onto the panel, where the full-width rule finds it', async () => {
    await mountTerminal();
    const panel = host.querySelector('aside.oa-panel');
    expect(panel?.classList.contains('oa-terminal-panel')).toBe(true);
  });

  it('prints the spec banner as the first block', async () => {
    await mountTerminal();
    const banner = host.querySelector('.oa-terminal-block-banner');
    expect(banner?.textContent).toContain(BANNER);
  });

  it('echoes a submitted line and shows its output', async () => {
    mockExec('42 users');
    await mountTerminal();

    const textarea = host.querySelector<HTMLTextAreaElement>('.oa-terminal-input')!;
    input(textarea, 'user list');
    key(textarea, 'Enter');
    await settle();
    await settle();

    const blocks = Array.from(host.querySelectorAll('.oa-terminal-block'));
    expect(blocks.some((block) => block.classList.contains('oa-terminal-block-echo') && block.textContent?.includes('user list'))).toBe(true);
    expect(blocks.some((block) => block.classList.contains('oa-terminal-block-output') && block.textContent?.includes('42 users'))).toBe(true);
    expect(vi.mocked(execConsoleLine)).toHaveBeenCalledOnce();
    // The draft clears once the line is sent, the way a shell's prompt does.
    expect(textarea.value).toBe('');
  });

  it('renders output incrementally across multiple stream chunks without truncation', async () => {
    vi.mocked(execConsoleLine).mockImplementation(async (_request, handlers: ConsoleExecHandlers) => {
      handlers.onOut?.('Accounts\n');
      await nextTick();
      handlers.onOut?.('Groups\n');
      await nextTick();
      handlers.onOut?.('Session\n');
      handlers.onDone?.({ ok: true, code: '', exit: false, elapsed_ms: 20, json: false, lang: 'en' });
    });
    await mountTerminal();

    const textarea = host.querySelector<HTMLTextAreaElement>('.oa-terminal-input')!;
    input(textarea, 'help');
    key(textarea, 'Enter');
    await settle();
    await settle();

    const outputBlock = host.querySelector('.oa-terminal-block-output');
    expect(outputBlock?.textContent).toContain('Accounts');
    expect(outputBlock?.textContent).toContain('Groups');
    expect(outputBlock?.textContent).toContain('Session');
  });

  it('keeps the tab strip inside the card at ten tabs, with the settings button always reachable', async () => {
    await mountTerminal();

    const addButton = host.querySelector<HTMLButtonElement>('.oa-terminal-tab-add')!;
    for (let i = 0; i < 9; i += 1) {
      addButton.click();
      await nextTick();
    }
    expect(host.querySelectorAll('.oa-terminal-tab')).toHaveLength(10);

    const strip = host.querySelector('.oa-terminal-tabstrip')!;
    const scroller = host.querySelector('.oa-terminal-tabs')!;
    const settingsButton = host.querySelector('.oa-terminal-settings-btn')!;
    const stillAddButton = host.querySelector('.oa-terminal-tab-add')!;

    // Pinned outside the scrolling row — a sibling, not a descendant — so it
    // stays reachable regardless of how many tabs overflow the scroller.
    expect(scroller.contains(settingsButton)).toBe(false);
    expect(strip.contains(settingsButton)).toBe(true);
    // "+" lives inside the same scroller as the tabs, so it scrolls with them.
    expect(scroller.contains(stillAddButton)).toBe(true);

    // The layout rule that makes tabs shrink instead of pushing the strip
    // wider than its card, read from source: jsdom never lays anything out,
    // so a `scrollWidth` assertion here would only be measuring zeroes.
    // `styles.test.ts` already covers the equivalent built-CSS assertion for
    // screens that need one; this one does not depend on `make web` having
    // just run.
    const source = fs.readFileSync(path.resolve(__dirname, '../src/styles/_terminal.scss'), 'utf8');
    // The pill is the flex item, so the sizing rule is on the group, not the tab.
    const tabRule = /\.oa-terminal-tab-group\s*\{[^}]*\}/.exec(source)?.[0] ?? '';
    expect(tabRule).toMatch(/flex:\s*0 1 150px/);
    expect(tabRule).toMatch(/min-width:\s*64px/);
    // The focus ring moved to the pill with the tab itself; without this rule
    // a keyboard user has no visible focus at all.
    expect(source).toMatch(/\.oa-terminal-tab-group:has\(\.oa-terminal-tab:focus-visible\)/);
    const scrollerRule = /\.oa-terminal-tabs\s*\{[^}]*\}/.exec(source)?.[0] ?? '';
    expect(scrollerRule).toMatch(/overflow-x:\s*auto/);
  });

  it('renames a tab on double-click, cancels a rename with Escape, and closes a tab with its ×', async () => {
    await mountTerminal();
    host.querySelector<HTMLButtonElement>('.oa-terminal-tab-add')!.click();
    await nextTick();
    expect(host.querySelectorAll('.oa-terminal-tab')).toHaveLength(2);

    const firstTab = host.querySelectorAll<HTMLElement>('.oa-terminal-tab')[0]!;
    firstTab.dispatchEvent(new MouseEvent('dblclick', { bubbles: true }));
    await nextTick();
    // The field sits beside the tab in its pill, not inside the tab role.
    const renameInput = host.querySelector<HTMLInputElement>('.oa-terminal-tab-rename input')!;
    input(renameInput, 'Diagnostics');
    key(renameInput, 'Enter');
    await nextTick();
    expect(firstTab.querySelector('.oa-terminal-tab-label')?.textContent).toBe('Diagnostics');

    // A second rename, abandoned with Escape, leaves the committed title alone.
    firstTab.dispatchEvent(new MouseEvent('dblclick', { bubbles: true }));
    await nextTick();
    const secondAttempt = host.querySelector<HTMLInputElement>('.oa-terminal-tab-rename input')!;
    input(secondAttempt, 'Abandoned');
    key(secondAttempt, 'Escape');
    await nextTick();
    expect(firstTab.querySelector('.oa-terminal-tab-label')?.textContent).toBe('Diagnostics');

    const closeButtons = host.querySelectorAll<HTMLButtonElement>('.oa-terminal-tab-close');
    closeButtons[1]!.click();
    await nextTick();
    expect(host.querySelectorAll('.oa-terminal-tab')).toHaveLength(1);
    expect(host.querySelector('.oa-terminal-tab-label')?.textContent).toBe('Diagnostics');
  });

  it('keeps the × and the rename field out of the tab role, so a screen reader sees one control per tab', async () => {
    await mountTerminal();
    const tab = host.querySelector<HTMLElement>('.oa-terminal-tab')!;
    expect(tab.querySelector('button, input')).toBeNull();

    // Still true mid-rename: the field is the pill's, not the tab's.
    tab.dispatchEvent(new MouseEvent('dblclick', { bubbles: true }));
    await nextTick();
    expect(host.querySelector('.oa-terminal-tab-rename input')).not.toBeNull();
    expect(host.querySelector('[role="tab"] input, [role="tab"] button')).toBeNull();
  });

  it('opens a fresh tab when the only one closes, rather than leaving an empty screen', async () => {
    await mountTerminal();
    expect(host.querySelectorAll('.oa-terminal-tab')).toHaveLength(1);

    host.querySelector<HTMLButtonElement>('.oa-terminal-tab-close')!.click();
    await nextTick();

    const tabs = host.querySelectorAll('.oa-terminal-tab');
    expect(tabs).toHaveLength(1);
    // A new tab, not the one that just closed still lingering: its default
    // label numbers past it, since tab serials are never reused.
    expect(tabs[0]?.querySelector('.oa-terminal-tab-label')?.textContent).toBe(t('terminalTabDefault', { n: 2 }));
    expect(host.querySelector('.oa-terminal-input')).not.toBeNull();
  });

  it('opens appearance as a column beside the terminal, previewing live and persisting on commit', async () => {
    await mountTerminal();

    host.querySelector<HTMLButtonElement>('.oa-terminal-settings-btn')!.click();
    await nextTick();
    await new Promise(requestAnimationFrame);

    // A column of the chat row, not a sheet over the terminal: it is a
    // sibling of the terminal's own card rather than a descendant of it,
    // which is the whole difference between the two.
    const panel = Array.from(host.querySelectorAll<HTMLElement>('.oa-panel'))
      .find((node) => node.querySelector('.oa-terminal-settings')) ?? null;
    expect(panel).not.toBeNull();
    const terminal = host.querySelector<HTMLElement>('.oa-terminal')!;
    expect(terminal.contains(panel)).toBe(false);
    expect(panel!.querySelector('.oa-terminal-settings')).not.toBeNull();

    expect(terminal.style.getPropertyValue('--oa-term-font-size')).toBe('13px');

    const fontSizeRange = host.querySelector<HTMLInputElement>('.oa-terminal-settings input[type="range"]')!;
    fontSizeRange.value = '18';
    fontSizeRange.dispatchEvent(new Event('input', { bubbles: true }));
    await nextTick();
    // Live preview: applied to the page before anything is written to disk.
    expect(terminal.style.getPropertyValue('--oa-term-font-size')).toBe('18px');
    expect(localStorage.getItem('obsidian-arc-terminal-prefs')).toBeNull();

    fontSizeRange.dispatchEvent(new Event('change', { bubbles: true }));
    await nextTick();
    const stored = JSON.parse(localStorage.getItem('obsidian-arc-terminal-prefs') ?? '{}') as { fontSize?: number };
    expect(stored.fontSize).toBe(18);

    // Using a field does not dismiss the column. It never could, now that it
    // is a column — which is the point of it being one.
    expect(host.querySelector('.oa-panel')).not.toBeNull();
  });

  it('stops following when the reader scrolls up, so output that arrives after does not pull them back', async () => {
    let finish: (() => void) | undefined;
    vi.mocked(execConsoleLine).mockImplementation((_request, handlers: ConsoleExecHandlers) => new Promise<void>((resolve) => {
      finish = () => {
        handlers.onOut?.('tick\n');
        handlers.onDone?.({ ok: true, code: '', exit: false, elapsed_ms: 1, json: false, lang: 'en' });
        resolve();
      };
    }));
    await mountTerminal();

    // jsdom has no layout: a 1000px run of text in a 200px window.
    const scroller = host.querySelector<HTMLElement>('.oa-terminal-scroll')!;
    Object.defineProperty(scroller, 'scrollHeight', { configurable: true, value: 1000 });
    Object.defineProperty(scroller, 'clientHeight', { configurable: true, value: 200 });
    Object.defineProperty(scroller, 'scrollTop', { configurable: true, writable: true, value: 0 });

    const textarea = host.querySelector<HTMLTextAreaElement>('.oa-terminal-input')!;
    input(textarea, 'watch usage rpm');
    key(textarea, 'Enter');
    await settle();
    // Submitting always follows the new line down.
    expect(scroller.scrollTop).toBe(1000);

    scroller.scrollTop = 0;
    scroller.dispatchEvent(new Event('scroll'));
    await settle();
    expect(host.querySelector('.oa-terminal-jump')).not.toBeNull();

    finish!();
    await settle();
    await settle();
    expect(scroller.scrollTop).toBe(0);
  });

  it('does not submit on the Enter that commits an IME candidate', async () => {
    await mountTerminal();

    const textarea = host.querySelector<HTMLTextAreaElement>('.oa-terminal-input')!;
    input(textarea, 'user list');
    const enter = new KeyboardEvent('keydown', { key: 'Enter', bubbles: true, cancelable: true });
    // Safari sends the committing keydown after compositionend, so it is not
    // isComposing any more; only the legacy keyCode still says it was pinyin.
    Object.defineProperty(enter, 'keyCode', { value: 229 });
    textarea.dispatchEvent(enter);
    await settle();

    expect(vi.mocked(execConsoleLine)).not.toHaveBeenCalled();
    expect(textarea.value).toBe('user list');
  });

  it('lets Shift+Tab leave the prompt, while plain Tab keeps completing', async () => {
    await mountTerminal();

    const textarea = host.querySelector<HTMLTextAreaElement>('.oa-terminal-input')!;
    const shiftTab = new KeyboardEvent('keydown', { key: 'Tab', shiftKey: true, bubbles: true, cancelable: true });
    textarea.dispatchEvent(shiftTab);
    expect(shiftTab.defaultPrevented).toBe(false);

    const plainTab = new KeyboardEvent('keydown', { key: 'Tab', bubbles: true, cancelable: true });
    textarea.dispatchEvent(plainTab);
    expect(plainTab.defaultPrevented).toBe(true);
  });

  it('drops a completion whose draft was edited while it was on its way back', async () => {
    let answer: ((reply: { from: number; items: Array<{ value: string; label: string; hint: string }> }) => void) | undefined;
    vi.mocked(completeConsoleLine).mockImplementationOnce(() => new Promise((resolve) => {
      answer = resolve;
    }));
    await mountTerminal();

    const textarea = host.querySelector<HTMLTextAreaElement>('.oa-terminal-input')!;
    input(textarea, 'git sta');
    textarea.setSelectionRange(7, 7);
    key(textarea, 'Tab');
    await nextTick();

    // The reader keeps typing before the reply arrives.
    input(textarea, 'git stat');
    answer!({ from: 4, items: [{ value: 'status', label: 'status', hint: '' }] });
    await settle();

    expect(textarea.value).toBe('git stat');
  });

  it('opens the custom font field when Custom is chosen from a preset, without changing the font', async () => {
    await mountTerminal();

    host.querySelector<HTMLButtonElement>('.oa-terminal-settings-btn')!.click();
    await nextTick();
    await new Promise(requestAnimationFrame);

    const panel = Array.from(host.querySelectorAll<HTMLElement>('.oa-panel'))
      .find((node) => node.querySelector('.oa-terminal-settings'))!;
    const terminal = host.querySelector<HTMLElement>('.oa-terminal')!;
    expect(panel.querySelectorAll('input[type="text"]')).toHaveLength(0);

    panel.querySelector<HTMLButtonElement>('.oa-select')!.click();
    await nextTick();
    const custom = Array.from(document.querySelectorAll<HTMLElement>('.oa-select-menu .oa-menu-item'))
      .find((item) => item.textContent?.includes(t('terminalFontCustom')));
    expect(custom).toBeDefined();
    custom!.click();
    await settle();

    // Choosing it opens the field on the stack in use, and the terminal keeps that font.
    expect(terminal.style.getPropertyValue('--oa-term-font-family')).toBe(DEFAULT_TERMINAL_FONT_FAMILY);
    const field = panel.querySelector<HTMLInputElement>('input[type="text"]');
    expect(field?.value).toBe(DEFAULT_TERMINAL_FONT_FAMILY);
    // The cap is held while typing. A blank field is shown as typed but never
    // committed, so the stored stack is always one that reloads.
    expect(field?.maxLength).toBe(FONT_FAMILY_MAX_LENGTH);

    input(field!, '"Iosevka", monospace');
    await nextTick();
    expect(terminal.style.getPropertyValue('--oa-term-font-family')).toBe('"Iosevka", monospace');
    const stored = JSON.parse(localStorage.getItem('obsidian-arc-terminal-prefs') ?? '{}') as { fontFamily?: string };
    expect(stored.fontFamily).toBe('"Iosevka", monospace');
  });

  it('keeps the last usable font when the custom field is cleared, and stores nothing blank', async () => {
    await mountTerminal();

    host.querySelector<HTMLButtonElement>('.oa-terminal-settings-btn')!.click();
    await nextTick();
    await new Promise(requestAnimationFrame);

    const panel = Array.from(host.querySelectorAll<HTMLElement>('.oa-panel'))
      .find((node) => node.querySelector('.oa-terminal-settings'))!;
    const terminal = host.querySelector<HTMLElement>('.oa-terminal')!;

    panel.querySelector<HTMLButtonElement>('.oa-select')!.click();
    await nextTick();
    Array.from(document.querySelectorAll<HTMLElement>('.oa-select-menu .oa-menu-item'))
      .find((item) => item.textContent?.includes(t('terminalFontCustom')))!
      .click();
    await settle();

    const field = panel.querySelector<HTMLInputElement>('input[type="text"]')!;
    input(field, '"Iosevka", monospace');
    await nextTick();

    input(field, '');
    await nextTick();
    // The field stays blank so the user can retype it; the terminal and the
    // stored prefs keep the stack that was last usable.
    expect(field.value).toBe('');
    expect(terminal.style.getPropertyValue('--oa-term-font-family')).toBe('"Iosevka", monospace');
    const stored = JSON.parse(localStorage.getItem('obsidian-arc-terminal-prefs') ?? '{}') as { fontFamily?: string };
    expect(stored.fontFamily).toBe('"Iosevka", monospace');

    input(field, '   ');
    await nextTick();
    expect(terminal.style.getPropertyValue('--oa-term-font-family')).toBe('"Iosevka", monospace');
  });

  it('sizes the server table width from the text box on every path, letter spacing included', async () => {
    // jsdom has no layout. The scroller gets the width and padding that
    // _terminal.scss gives it, and the resize observer fires once on observe,
    // as a browser does.
    Object.defineProperty(HTMLElement.prototype, 'clientWidth', {
      configurable: true,
      get(this: HTMLElement) {
        return this.classList.contains('oa-terminal-scroll') ? 532 : 0;
      },
    });
    class FiringResizeObserver {
      constructor(private readonly callback: ResizeObserverCallback) {}
      observe(target: Element): void {
        if (target.classList.contains('oa-terminal-scroll')) {
          (target as HTMLElement).style.padding = '14px 16px';
        }
        this.callback([{ target, contentRect: { width: 500 } } as unknown as ResizeObserverEntry], this as unknown as ResizeObserver);
      }
      unobserve(): void {}
      disconnect(): void {}
    }
    vi.stubGlobal('ResizeObserver', FiringResizeObserver);

    try {
      await mountTerminal();
      const textarea = host.querySelector<HTMLTextAreaElement>('.oa-terminal-input')!;
      const lastColumns = (): number => {
        const calls = vi.mocked(execConsoleLine).mock.calls;
        return calls[calls.length - 1]![0].cols;
      };
      const runLine = async (): Promise<void> => {
        input(textarea, 'user list');
        key(textarea, 'Enter');
        await settle();
      };

      // 500px of text at 13px.
      await runLine();
      expect(lastColumns()).toBe(63);

      host.querySelector<HTMLButtonElement>('.oa-terminal-settings-btn')!.click();
      await nextTick();
      await new Promise(requestAnimationFrame);
      const ranges = host.querySelectorAll<HTMLInputElement>('.oa-terminal-settings input[type="range"]');

      // 14px: the same 500px holds 59 columns, not the 63 the client width would give.
      ranges[0]!.value = '14';
      ranges[0]!.dispatchEvent(new Event('input', { bubbles: true }));
      await nextTick();
      await runLine();
      expect(lastColumns()).toBe(59);

      // Letter spacing adds to every advance: 8.42px + 1px per column.
      ranges[2]!.value = '1';
      ranges[2]!.dispatchEvent(new Event('input', { bubbles: true }));
      await nextTick();
      await runLine();
      expect(lastColumns()).toBe(53);
    } finally {
      vi.unstubAllGlobals();
      delete (HTMLElement.prototype as unknown as Record<string, unknown>).clientWidth;
    }
  });

  it('mounts and unmounts cleanly, aborting anything still running', async () => {
    // Still "running" when the page goes away — the shape teardown has to
    // survive without throwing.
    vi.mocked(execConsoleLine).mockImplementation(() => new Promise(() => {}));

    await mountTerminal();
    host.querySelector<HTMLButtonElement>('.oa-terminal-settings-btn')!.click();
    await nextTick();
    host.querySelector<HTMLButtonElement>('.oa-terminal-tab-add')!.click();
    await nextTick();

    const textarea = host.querySelector<HTMLTextAreaElement>('.oa-terminal-input')!;
    input(textarea, 'dash');
    key(textarea, 'Enter');
    await nextTick();

    expect(() => app?.unmount()).not.toThrow();
    app = undefined;
    const signal = vi.mocked(execConsoleLine).mock.calls[0]?.[2];
    expect(signal?.aborted).toBe(true);
    expect(host.textContent).toBe('');
  });

  it('reaches a tab from the keyboard: Enter selects it, the arrows walk the strip, F2 renames it', async () => {
    await mountTerminal();
    host.querySelector<HTMLButtonElement>('.oa-terminal-tab-add')!.click();
    await nextTick();
    const [first, second] = host.querySelectorAll<HTMLElement>('.oa-terminal-tab');
    expect(second!.getAttribute('aria-selected')).toBe('true');
    expect(first!.getAttribute('tabindex')).toBe('-1');

    first!.focus();
    // The active fill is the pill's, so the check reads the pill around the tab.
    const pillOf = (tab: HTMLElement): HTMLElement => tab.closest<HTMLElement>('.oa-terminal-tab-group')!;
    key(first!, 'Enter');
    await nextTick();
    expect(pillOf(first!).classList.contains('active')).toBe(true);
    expect(first!.getAttribute('aria-selected')).toBe('true');
    expect(second!.getAttribute('aria-selected')).toBe('false');

    key(first!, 'ArrowRight');
    await nextTick();
    expect(pillOf(second!).classList.contains('active')).toBe(true);
    expect(document.activeElement).toBe(second);

    key(second!, 'F2');
    await nextTick();
    expect(host.querySelector('.oa-terminal-tab-rename input')).not.toBeNull();
  });

  it('reads Alt+T and Alt+W by the physical key, since Option turns the letter into a symbol on macOS', async () => {
    await mountTerminal();
    expect(host.querySelectorAll('.oa-terminal-tab')).toHaveLength(1);

    // What Option+T and Option+W report on a Mac: the symbol in `key`, the
    // letter's key in `code`.
    document.body.dispatchEvent(new KeyboardEvent('keydown', { key: '†', code: 'KeyT', altKey: true, bubbles: true }));
    await nextTick();
    expect(host.querySelectorAll('.oa-terminal-tab')).toHaveLength(2);

    document.body.dispatchEvent(new KeyboardEvent('keydown', { key: '∑', code: 'KeyW', altKey: true, bubbles: true }));
    await nextTick();
    expect(host.querySelectorAll('.oa-terminal-tab')).toHaveLength(1);
  });
});
