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
import { attachOverlayScrollbar } from './scrollbar';

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
  toggleFullscreen(onDone?: () => void): boolean;
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

/** Dismisses whatever panel is currently open in the given host, if any. */
export function closePanel(host: HTMLElement): void {
  openPanels.get(host)?.close();
}

export function openPanel(options: PanelOptions): PanelHandle {
  // A panel already open means one record is being swapped for another rather
  // than a column arriving. Read that before closing it, which takes the class
  // away: the new panel then takes the old one's place without animating,
  // instead of flashing the list wide for a slide out and a slide back in.
  const replacing = Array.from(options.host.children).some(
    (child) => child instanceof HTMLElement && child.classList.contains('oa-panel') && child.classList.contains('open'),
  );

  const existing = openPanels.get(options.host);
  if (existing) {
    existing.close();
  }
  // Immediately clean up any previous panel element so two panel columns never coexist in flex
  for (const child of Array.from(options.host.children)) {
    if (child instanceof HTMLElement && child.classList.contains('oa-panel')) {
      child.remove();
    }
  }

  if (window.getComputedStyle(options.host).position === 'static') {
    options.host.style.position = 'relative';
  }

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

  const bodyWrap = el('div', 'oa-panel-body-wrap');
  const body = el('div', 'oa-panel-body');
  bodyWrap.appendChild(body);
  const scrollbar = attachOverlayScrollbar(body, bodyWrap);
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
      scrollbar.destroy();
      openPanels.delete(options.host);
      document.removeEventListener('keydown', onKey);
      // Slide out before removing, so the column is seen leaving rather than
      // vanishing and snapping the content wider.
      panel.classList.remove('open');
      window.setTimeout(() => {
        panel.remove();
        // After it has gone, not when it was asked to go. Every caller here
        // navigates in this callback, and a navigation replaces the whole
        // root — which took the panel with it before it had moved a pixel,
        // and made the animation above look like it did not exist.
        options.onClose?.();
      }, 340);
    },
    rebuild() {
      body.textContent = '';
      body.scrollTop = 0;
      options.build(body, handle);
      body.appendChild(error);
      scrollbar.update();
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
    toggleFullscreen(onDone?: () => void) {
      return zoom(!fullscreen, onDone);
    },
    isFullscreen() {
      return fullscreen;
    },
  };

  // --- full screen ----------------------------------------------------------
  //
  // Full screen means absolute over the row, expanding smoothly from the right
  // edge of the host. The right boundary remains anchored (right: 0, left: auto)
  // while width transitions between the drawer width and 100%, avoiding any
  // coordinate drift or sudden horizontal shifts across the screen.

  let fullscreen = false;
  let zoomTimer = 0;
  let swapTimer = 0;

  const ZOOM_MS = 340;
  /**
   * When the interior swaps layouts, partway through the frame's travel.
   *
   * The panel's contents are laid out by `.fullscreen` — drawer gets tabs
   * across the top, full screen gets a rail down the side — and a class
   * cannot be interpolated, so that swap is always one frame. Doing it at the
   * end meant the frame glided open and then the inside of it jumped, which
   * is the worst place to put it: the eye has just finished following a
   * smooth movement and is looking straight at the thing that snaps.
   *
   * So it happens early, while the frame is still visibly moving and the
   * contents are faded out for it. What is left to see is one continuous
   * movement with the contents dissolving from one arrangement to the other
   * inside it.
   */
  const SWAP_AT_MS = 130;

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

  /**
   * Freezes siblings at their target drawer width while the panel shrinks back
   * to a drawer column, so the content behind it is already at its final width
   * and does not reflow or snap when the animation finishes.
   */
  function freezeSiblingsForDrawer(drawerWidth: number): () => void {
    const pinned: Array<[HTMLElement, string]> = [];
    const children = Array.from(options.host.children).filter(
      (c): c is HTMLElement => c !== panel && c instanceof HTMLElement,
    );
    if (children.length === 0) return () => {};

    const hostStyle = window.getComputedStyle(options.host);
    const gap = parseFloat(hostStyle.gap) || 0;
    const hostWidth = options.host.getBoundingClientRect().width;

    let fixedTotal = 0;
    const flexible: HTMLElement[] = [];
    for (const child of children) {
      pinned.push([child, child.style.flex]);
      const style = window.getComputedStyle(child);
      if (parseFloat(style.flexGrow) > 0) {
        flexible.push(child);
      } else {
        fixedTotal += child.getBoundingClientRect().width;
      }
    }

    const totalGaps = children.length * gap;
    const remaining = Math.max(0, hostWidth - drawerWidth - fixedTotal - totalGaps);
    const perFlex = flexible.length > 0 ? remaining / flexible.length : remaining;

    for (const child of children) {
      if (flexible.includes(child)) {
        child.style.flex = `0 0 ${perFlex}px`;
      } else {
        child.style.flex = `0 0 ${child.getBoundingClientRect().width}px`;
      }
    }

    return () => {
      for (const [node, previous] of pinned) node.style.flex = previous;
    };
  }

  function zoom(next: boolean, onDone?: () => void): boolean {
    if (next === fullscreen) return fullscreen;

    window.clearTimeout(zoomTimer);
    window.clearTimeout(swapTimer);
    const drawerWidth = storedWidth(PANEL_WIDTH_KEY, options.width ?? 400);

    // The frame's own geometry is pinned inline for the whole animation, so
    // adding or removing `.fullscreen` partway through cannot disturb it:
    // inline styles outrank the class either way. That is what lets the
    // contents change over while the frame is still travelling.
    const clearGeometry = (): void => {
      panel.style.top = '';
      panel.style.bottom = '';
      panel.style.right = '';
      panel.style.left = '';
      panel.style.width = '';
    };
    const pinGeometry = (width: string): void => {
      panel.style.top = '0';
      panel.style.bottom = '0';
      panel.style.right = '0';
      panel.style.left = 'auto';
      panel.style.width = width;
    };

    if (next) {
      const release = freezeSiblings();
      const currentWidth = panel.getBoundingClientRect().width || drawerWidth;

      panel.classList.add('zooming', 'swapping');
      pinGeometry(`${currentWidth}px`);

      void panel.offsetWidth; // Force reflow to commit initial width
      panel.style.width = '100%';
      fullscreen = true;

      swapTimer = window.setTimeout(() => {
        panel.classList.add('fullscreen');
        panel.classList.remove('swapping');
      }, SWAP_AT_MS);

      zoomTimer = window.setTimeout(() => {
        panel.classList.remove('zooming');
        clearGeometry();
        release();
        onDone?.();
      }, ZOOM_MS + 20);
    } else {
      const release = freezeSiblingsForDrawer(drawerWidth);

      panel.classList.add('zooming', 'swapping');
      pinGeometry('100%');

      void panel.offsetWidth; // Force reflow to commit 100% width
      panel.style.width = `${drawerWidth}px`;
      fullscreen = false;

      swapTimer = window.setTimeout(() => {
        panel.classList.remove('fullscreen');
        panel.classList.remove('swapping');
      }, SWAP_AT_MS);

      zoomTimer = window.setTimeout(() => {
        panel.classList.remove('zooming');
        clearGeometry();
        release();
        onDone?.();
      }, ZOOM_MS + 20);
    }

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
  panel.appendChild(bodyWrap);
  if (wantsFooter) panel.appendChild(footer);
  options.host.appendChild(panel);
  openPanels.set(options.host, handle);

  if (replacing) {
    // The column it is taking over is already at full width, so it opens where
    // that one stood — before anything is painted, and therefore without a
    // frame in which the list behind it is briefly wide again.
    panel.classList.add('open');
  } else {
    // Force reflow so the starting state is committed before the transition begins.
    void panel.offsetWidth;
    requestAnimationFrame(() => panel.classList.add('open'));
  }
  return handle;
}
