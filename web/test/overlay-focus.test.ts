// A sheet is a modal dialog: it names itself as one, takes focus when it
// opens, keeps Tab inside itself, hands focus back when it goes, and closes on
// a backdrop click only when the press began on the backdrop too.

import { afterEach, describe, expect, it, vi } from 'vitest';
import { createApp, h, nextTick, ref, type App as VueApp, type Ref } from 'vue';
import OaOverlay from '../src/components/OaOverlay.vue';
import { wait } from './helpers';

const PAST_THE_FADE_MS = 260;
const nextFrame = (): Promise<void> => new Promise((resolve) => requestAnimationFrame(() => resolve()));

describe('OaOverlay as a modal dialog', () => {
  let app: VueApp | null = null;
  let host: HTMLElement | null = null;
  let opener: HTMLButtonElement | null = null;

  afterEach(() => {
    app?.unmount();
    app = null;
    host?.remove();
    host = null;
    opener?.remove();
    opener = null;
    vi.restoreAllMocks();
  });

  function mountSheet(onClose: () => void = () => {}): { show: Ref<boolean>; overlay: () => HTMLElement } {
    opener = document.createElement('button');
    opener.type = 'button';
    document.body.appendChild(opener);
    opener.focus();

    const root = document.createElement('div');
    document.body.appendChild(root);
    host = root;
    const show = ref(true);
    app = createApp({
      render: () => (show.value
        ? h(OaOverlay, { overlayClass: 'test-sheet', label: 'Notice', onClose }, {
          default: () => [
            h('button', { type: 'button', id: 'first' }, 'First'),
            h('button', { type: 'button', id: 'last' }, 'Last'),
          ],
        })
        : null),
    });
    app.mount(root);
    return { show, overlay: () => document.querySelector<HTMLElement>('.test-sheet')! };
  }

  function tab(shiftKey = false): KeyboardEvent {
    const event = new KeyboardEvent('keydown', { key: 'Tab', shiftKey, bubbles: true, cancelable: true });
    document.dispatchEvent(event);
    return event;
  }

  it('names itself as a dialog and says it is modal', () => {
    const { overlay } = mountSheet();
    expect(overlay().getAttribute('role')).toBe('dialog');
    expect(overlay().getAttribute('aria-modal')).toBe('true');
    expect(overlay().getAttribute('aria-label')).toBe('Notice');
  });

  it('moves focus into the sheet once it is shown', async () => {
    mountSheet();
    expect(document.activeElement).toBe(opener);
    await nextFrame();
    expect(document.activeElement?.id).toBe('first');
  });

  it('keeps Tab and Shift+Tab inside the sheet', async () => {
    const { overlay } = mountSheet();
    await nextFrame();
    const first = overlay().querySelector<HTMLElement>('#first')!;
    const last = overlay().querySelector<HTMLElement>('#last')!;

    last.focus();
    const forward = tab();
    expect(forward.defaultPrevented).toBe(true);
    expect(document.activeElement).toBe(first);

    const backward = tab(true);
    expect(backward.defaultPrevented).toBe(true);
    expect(document.activeElement).toBe(last);
  });

  it('hands focus back to what had it when it goes', async () => {
    const { show } = mountSheet();
    await nextFrame();
    expect(document.activeElement?.id).toBe('first');

    show.value = false;
    await nextTick();
    expect(document.activeElement).toBe(opener);
  });

  it('keeps Tab in the top sheet when another sheet is open beneath it', async () => {
    opener = document.createElement('button');
    document.body.appendChild(opener);
    const root = document.createElement('div');
    document.body.appendChild(root);
    host = root;
    const controls = (prefix: string): Array<ReturnType<typeof h>> => [
      h('button', { type: 'button', id: `${prefix}-first` }, 'First'),
      h('button', { type: 'button', id: `${prefix}-last` }, 'Last'),
    ];
    app = createApp({
      render: () => [
        h(OaOverlay, { overlayClass: 'lower-sheet', label: 'Lower' }, { default: () => controls('lower') }),
        h(OaOverlay, { overlayClass: 'upper-sheet', label: 'Upper' }, { default: () => controls('upper') }),
      ],
    });
    app.mount(root);
    await nextFrame();

    const focused: string[] = [];
    const record = (event: FocusEvent): void => { focused.push((event.target as HTMLElement).id); };
    document.addEventListener('focusin', record);
    try {
      document.querySelector<HTMLElement>('#upper-last')!.focus();
      const wrap = tab();
      expect(wrap.defaultPrevented).toBe(true);
      expect(document.activeElement?.id).toBe('upper-first');

      // Tab from a control that is not the last one is the browser's to move,
      // so the sheet must not intervene at all.
      const next = tab();
      expect(next.defaultPrevented).toBe(false);
      expect(focused).toEqual(['upper-last', 'upper-first']);
    } finally {
      document.removeEventListener('focusin', record);
    }
  });

  it('does not take focus back when focus has moved on before it goes', async () => {
    const { show } = mountSheet();
    await nextFrame();
    const elsewhere = document.createElement('button');
    document.body.appendChild(elsewhere);
    elsewhere.focus();

    show.value = false;
    await nextTick();
    expect(document.activeElement).toBe(elsewhere);
    elsewhere.remove();
  });

  it('does not close when a drag starts inside the card and ends on the backdrop', async () => {
    const onClose = vi.fn();
    const { overlay } = mountSheet(onClose);
    await nextFrame();

    overlay().querySelector<HTMLElement>('#first')!.dispatchEvent(new MouseEvent('pointerdown', { bubbles: true }));
    overlay().dispatchEvent(new MouseEvent('click', { bubbles: true }));
    await wait(PAST_THE_FADE_MS);
    expect(onClose).not.toHaveBeenCalled();
  });

  it('closes on a press and a click that both land on the backdrop', async () => {
    const onClose = vi.fn();
    const { overlay } = mountSheet(onClose);
    await nextFrame();

    overlay().dispatchEvent(new MouseEvent('pointerdown', { bubbles: true }));
    overlay().dispatchEvent(new MouseEvent('click', { bubbles: true }));
    await wait(PAST_THE_FADE_MS);
    expect(onClose).toHaveBeenCalledTimes(1);
  });
});
