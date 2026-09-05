// The right-hand panel.
//
// It is a column in the layout, not a sheet over it: the same rounded 18px
// card as the rail and the content beside it, sliding in by margin the way
// the conversation rail slides out. The list stays fully visible and simply
// narrows, which is what makes editing one row of forty feel like staying in
// the same place rather than leaving it.
//
// Below the breakpoint where three columns will not fit, CSS turns it into an
// overlay — the same fallback the conversation rail already makes.

import { t } from '../i18n';
import { ICONS, button, el, iconButton } from './dom';
import { attachResizer, storedWidth } from './resizer';

export interface PanelHandle {
  /** The scrolling content area, so a caller can repaint part of it. */
  body: HTMLElement;
  close(): void;
  /** Redraws the body from the build function. */
  rebuild(): void;
  setBusy(busy: boolean): void;
  setError(message: string): void;
  setTitle(title: string): void;
  /** Covers the whole row instead of being a column of it. Returns the new state. */
  toggleFullscreen(): boolean;
  /** Whether it is currently covering the row — build() reads this to lay itself out. */
  isFullscreen(): boolean;
}

export interface PanelOptions {
  /** The flex row the panel becomes a column of. */
  host: HTMLElement;
  title: string;
  build(body: HTMLElement, panel: PanelHandle): void;
  confirmLabel?: string;
  onConfirm?(panel: PanelHandle): void | Promise<void>;
  cancelLabel?: string;
  /** Far left of the footer — "Delete", usually. */
  destructive?: {
    label: string;
    /**
     * The question to put in the footer before the action runs. Without one
     * the button acts on the first click.
     */
    confirm?: string;
    onSelect(panel: PanelHandle): void | Promise<void>;
  };
  /**
   * Drops the footer entirely. For a panel whose sections each save
   * themselves, where one Save button at the bottom would be claiming to
   * commit things it has nothing to do with.
   */
  footer?: boolean;
  /** Extra controls in the head, between the title and the close button. */
  actions?: HTMLElement[];
  /** Replaces the close button with a back arrow, for a panel opened from another. */
  onBack?(): void;
  /** Run when the panel is dismissed — by its button, by Escape, or by a caller. */
  onClose?(): void;
  width?: number;
}

// One panel per host. Opening a second closes the first rather than stacking
// columns until nothing is readable.
const openPanels = new WeakMap<HTMLElement, PanelHandle>();

const PANEL_WIDTH_KEY = 'obsidian-arc-panel-width';

export function openPanel(options: PanelOptions): PanelHandle {
  openPanels.get(options.host)?.close();

  // A width the user has dragged to wins over the caller's suggestion: they
  // set it while looking at one of these panels, and they are all the same
  // column.
  const width = storedWidth(PANEL_WIDTH_KEY, options.width ?? 400);
  const panel = el('aside', 'oa-panel');
  panel.style.setProperty('--oa-panel-width', `${width}px`);

  const head = el('div', 'oa-panel-head');
  if (options.onBack) {
    head.appendChild(iconButton('oa-icon-btn', ICONS.chevron, t('back'), () => options.onBack!(), 16));
  }
  const title = el('h2', 'oa-panel-title', options.title);
  head.appendChild(title);
  for (const action of options.actions ?? []) head.appendChild(action);
  head.appendChild(iconButton('oa-icon-btn', ICONS.close, t('close'), () => handle.close(), 16));

  const body = el('div', 'oa-panel-body');
  const error = el('p', 'oa-drawer-flash');

  const footer = el('div', 'oa-panel-foot');
  const cancelButton = button('oa-btn', options.cancelLabel ?? t('cancel'), () => handle.close());
  const confirmButton = button('oa-btn primary', options.confirmLabel ?? t('save'));

  let closed = false;

  const handle: PanelHandle = {
    body,
    close() {
      if (closed) return;
      closed = true;
      openPanels.delete(options.host);
      document.removeEventListener('keydown', onKey);
      // Slide out before removing, so the column is seen leaving rather than
      // vanishing and snapping the content wider.
      panel.classList.remove('open');
      window.setTimeout(() => panel.remove(), 340);
      options.onClose?.();
    },
    rebuild() {
      body.textContent = '';
      options.build(body, handle);
      body.appendChild(error);
    },
    setBusy(busy) {
      confirmButton.disabled = busy;
      cancelButton.disabled = busy;
      confirmButton.textContent = busy ? '…' : options.confirmLabel ?? t('save');
      // A destructive action that failed leaves its confirm row spinning on a
      // disabled button. Putting the normal footer back is what makes the
      // error it just set actionable.
      if (!busy && footer.classList.contains('confirming')) paintFooter();
    },
    setError(message) {
      error.textContent = message;
      error.classList.toggle('visible', !!message);
    },
    setTitle(next) {
      title.textContent = next;
    },
    toggleFullscreen() {
      return zoom(!fullscreen);
    },
    isFullscreen() {
      return fullscreen;
    },
  };

  // --- full screen ----------------------------------------------------------
  //
  // Full screen means absolute over the row, not a wider column: the list
  // beside it has nowhere left to shrink to. But absolute is not a state CSS
  // can transition into from a flex child, so the box is measured on both
  // sides of the switch and animated between the two by hand — the same
  // first/last trick a layout animation always comes down to.

  let fullscreen = false;
  let zoomTimer = 0;

  const ZOOM_MS = 340;

  /** The panel's box in the host's own coordinates, which is what left/top mean here. */
  function panelBox(): { left: number; top: number; width: number; height: number } {
    const host = options.host.getBoundingClientRect();
    const box = panel.getBoundingClientRect();
    return { left: box.left - host.left, top: box.top - host.top, width: box.width, height: box.height };
  }

  function applyBox(box: { left: number; top: number; width: number; height: number }): void {
    panel.style.left = `${box.left}px`;
    panel.style.top = `${box.top}px`;
    panel.style.width = `${box.width}px`;
    panel.style.height = `${box.height}px`;
  }

  /**
   * Pins the other columns at the width they have while the panel is one of
   * them. The panel leaves the flex flow the instant it goes full screen, and
   * without this the content behind it snaps wider before the panel has grown
   * far enough to cover it.
   */
  function freezeSiblings(): () => void {
    const pinned: Array<[HTMLElement, string]> = [];
    for (const child of Array.from(options.host.children)) {
      if (child === panel || !(child instanceof HTMLElement)) continue;
      pinned.push([child, child.style.flex]);
      child.style.flex = `0 0 ${child.getBoundingClientRect().width}px`;
    }
    return () => {
      for (const [node, previous] of pinned) node.style.flex = previous;
    };
  }

  function zoom(next: boolean): boolean {
    if (next === fullscreen) return fullscreen;
    const from = panelBox();

    // Freeze while the panel is still a column, whichever way it is going:
    // entering, that is now; leaving, that is after the class comes off.
    let release: () => void;
    if (next) {
      release = freezeSiblings();
      panel.classList.add('fullscreen');
    } else {
      panel.classList.remove('fullscreen');
      release = freezeSiblings();
    }
    fullscreen = next;

    const to = panelBox();

    panel.classList.add('zooming');
    applyBox(from);
    void panel.offsetWidth; // a start value for the transition to leave from
    applyBox(to);

    window.clearTimeout(zoomTimer);
    zoomTimer = window.setTimeout(() => {
      panel.classList.remove('zooming');
      panel.style.left = '';
      panel.style.top = '';
      panel.style.width = '';
      panel.style.height = '';
      release();
    }, ZOOM_MS + 20);

    return fullscreen;
  }

  function onKey(event: KeyboardEvent): void {
    // Escape closes, unless something inside it wants the key first — a
    // select that is open, for instance.
    if (event.key === 'Escape' && !event.defaultPrevented) handle.close();
  }
  document.addEventListener('keydown', onKey);

  const wantsFooter = options.footer !== false;
  if (wantsFooter) {
    if (options.onConfirm) {
      confirmButton.addEventListener('click', () => {
        handle.setError('');
        void options.onConfirm!(handle);
      });
    }
    paintFooter();
  }

  function paintFooter(): void {
    footer.textContent = '';
    footer.classList.remove('confirming');
    if (options.destructive) {
      footer.appendChild(button('oa-btn oa-btn-danger', options.destructive.label, () => {
        if (options.destructive!.confirm) armDestructive();
        else void options.destructive!.onSelect(handle);
      }));
    }
    footer.appendChild(el('span', 'oa-drawer-foot-spacer'));
    footer.appendChild(cancelButton);
    if (options.onConfirm) footer.appendChild(confirmButton);
  }

  /**
   * Asks in the footer rather than through window.confirm.
   *
   * The native dialog was the one piece of interface here that could not be
   * styled, and worse, a browser is free to suppress it: an embedded view, or
   * one where someone has ticked "prevent this page from creating more
   * dialogs", makes confirm() return false without showing anything. That
   * turns Delete into a button that silently does nothing, which is exactly
   * how it was found.
   */
  function armDestructive(): void {
    const destructive = options.destructive;
    if (!destructive) return;

    footer.textContent = '';
    footer.classList.add('confirming');
    footer.appendChild(el('p', 'oa-panel-confirm', destructive.confirm ?? destructive.label));

    const row = el('div', 'oa-panel-confirm-row');
    row.appendChild(el('span', 'oa-drawer-foot-spacer'));
    row.appendChild(button('oa-btn', t('cancel'), () => paintFooter()));

    const go = button('oa-btn oa-btn-danger-solid', destructive.label, () => {
      go.disabled = true;
      go.textContent = '…';
      void destructive.onSelect(handle);
    });
    row.appendChild(go);
    footer.appendChild(row);
  }

  attachResizer({
    target: panel,
    edge: 'left',
    cssVariable: '--oa-panel-width',
    styleTarget: panel,
    storageKey: PANEL_WIDTH_KEY,
    min: 320,
    max: 720,
    fallback: options.width ?? 400,
    label: t('resizePanel'),
  });

  handle.rebuild();
  panel.appendChild(head);
  panel.appendChild(body);
  if (wantsFooter) panel.appendChild(footer);
  options.host.appendChild(panel);
  openPanels.set(options.host, handle);

  // One frame closed, so the transition has a starting state to move from.
  requestAnimationFrame(() => panel.classList.add('open'));
  return handle;
}
