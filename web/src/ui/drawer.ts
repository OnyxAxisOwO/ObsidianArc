// The right-side drawer, generalised.
//
// The standalone build had exactly one of these — the settings panel — with
// its open/close wired by hand. The administration screens want the same
// thing for every edit form, so the behaviour lives here and the markup and
// classes are unchanged: the same 420px panel, the same 0.24s
// cubic-bezier(0.2, 0, 0, 1) slide, the same overlay.
//
// Editing in a drawer rather than a centre-screen modal is deliberate. The
// list stays visible beside it, so changing one row in a table of forty never
// loses your place.

import { ICONS, button, el, iconButton } from './dom';

export interface DrawerHandle {
  close(): void;
  /** Replaces the body, for a form that changes shape as it is filled in. */
  rebuild(): void;
  /** Disables the footer buttons while a save is in flight. */
  setBusy(busy: boolean): void;
  /** Shows a failure without closing the form the user just filled in. */
  setError(message: string): void;
}

export interface DrawerOptions {
  title: string;
  build(body: HTMLElement, drawer: DrawerHandle): void;
  /** The primary action. Omit for a read-only panel. */
  confirmLabel?: string;
  onConfirm?(drawer: DrawerHandle): void | Promise<void>;
  cancelLabel?: string;
  /** Rendered at the far left of the footer — "Delete", usually. */
  destructive?: { label: string; onSelect(drawer: DrawerHandle): void | Promise<void> };
  width?: number;
}

export function openDrawer(options: DrawerOptions): DrawerHandle {
  const overlay = el('div', 'oa-drawer-overlay');
  const panel = el('aside', 'oa-drawer');
  if (options.width) panel.style.width = `min(${options.width}px, 100vw)`;

  const head = el('div', 'oa-drawer-head');
  head.appendChild(el('h2', 'oa-drawer-title', options.title));
  const closeButton = iconButton('oa-icon-btn', ICONS.close, 'Close', () => handle.close(), 16);
  head.appendChild(closeButton);

  const body = el('div', 'oa-drawer-body');
  const error = el('p', 'oa-drawer-flash');

  const footer = el('div', 'oa-drawer-foot');
  const cancelButton = button('oa-btn', options.cancelLabel ?? 'Cancel', () => handle.close());
  const confirmButton = button('oa-btn primary', options.confirmLabel ?? 'Save');

  let closed = false;

  const handle: DrawerHandle = {
    close() {
      if (closed) return;
      closed = true;
      overlay.classList.remove('open');
      panel.classList.remove('open');
      // Removed after the slide rather than on the click, so the panel is
      // seen leaving instead of vanishing.
      window.setTimeout(() => {
        overlay.remove();
        panel.remove();
        document.removeEventListener('keydown', onKey);
      }, 220);
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
  };

  function onKey(event: KeyboardEvent): void {
    if (event.key === 'Escape') handle.close();
  }

  overlay.addEventListener('click', () => handle.close());
  document.addEventListener('keydown', onKey);

  if (options.destructive) {
    const remove = button('oa-btn oa-btn-danger', options.destructive.label, () => {
      void options.destructive!.onSelect(handle);
    });
    footer.appendChild(remove);
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

  handle.rebuild();
  panel.appendChild(head);
  panel.appendChild(body);
  panel.appendChild(footer);

  document.body.appendChild(overlay);
  document.body.appendChild(panel);
  // One frame before adding .open, so the transition has a starting state to
  // move from rather than snapping into place.
  requestAnimationFrame(() => {
    overlay.classList.add('open');
    panel.classList.add('open');
  });

  return handle;
}

/** A yes/no question, in the same panel as everything else. */
export function confirmDrawer(options: {
  title: string;
  body: string;
  confirmLabel: string;
  onConfirm(drawer: DrawerHandle): void | Promise<void>;
}): DrawerHandle {
  return openDrawer({
    title: options.title,
    confirmLabel: options.confirmLabel,
    build: (body) => {
      body.appendChild(el('p', 'oa-field-hint', options.body));
    },
    onConfirm: options.onConfirm,
  });
}
