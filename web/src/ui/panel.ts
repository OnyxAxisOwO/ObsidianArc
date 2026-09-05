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
  destructive?: { label: string; onSelect(panel: PanelHandle): void | Promise<void> };
  /** Replaces the close button with a back arrow, for a panel opened from another. */
  onBack?(): void;
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
    head.appendChild(iconButton('oa-icon-btn', ICONS.chevron, 'Back', () => options.onBack!(), 16));
  }
  const title = el('h2', 'oa-panel-title', options.title);
  head.appendChild(title);
  head.appendChild(iconButton('oa-icon-btn', ICONS.close, 'Close', () => handle.close(), 16));

  const body = el('div', 'oa-panel-body');
  const error = el('p', 'oa-drawer-flash');

  const footer = el('div', 'oa-panel-foot');
  const cancelButton = button('oa-btn', options.cancelLabel ?? 'Cancel', () => handle.close());
  const confirmButton = button('oa-btn primary', options.confirmLabel ?? 'Save');

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
    },
    rebuild() {
      body.textContent = '';
      options.build(body, handle);
      body.appendChild(error);
    },
    setBusy(busy) {
      confirmButton.disabled = busy;
      cancelButton.disabled = busy;
      confirmButton.textContent = busy ? '…' : options.confirmLabel ?? 'Save';
    },
    setError(message) {
      error.textContent = message;
      error.classList.toggle('visible', !!message);
    },
    setTitle(next) {
      title.textContent = next;
    },
  };

  function onKey(event: KeyboardEvent): void {
    // Escape closes, unless something inside it wants the key first — a
    // select that is open, for instance.
    if (event.key === 'Escape' && !event.defaultPrevented) handle.close();
  }
  document.addEventListener('keydown', onKey);

  if (options.destructive) {
    footer.appendChild(button('oa-btn oa-btn-danger', options.destructive.label, () => {
      void options.destructive!.onSelect(handle);
    }));
  }
  footer.appendChild(el('span', 'oa-drawer-foot-spacer'));
  footer.appendChild(cancelButton);
  if (options.onConfirm) {
    confirmButton.addEventListener('click', () => {
      handle.setError('');
      void options.onConfirm!(handle);
    });
    footer.appendChild(confirmButton);
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
    label: 'Resize the panel',
  });

  handle.rebuild();
  panel.appendChild(head);
  panel.appendChild(body);
  panel.appendChild(footer);
  options.host.appendChild(panel);
  openPanels.set(options.host, handle);

  // One frame closed, so the transition has a starting state to move from.
  requestAnimationFrame(() => panel.classList.add('open'));
  return handle;
}
