// The dropdown the model chip and the account button both use.
//
// The standalone build wired this by hand for its one menu. There are several
// now, so the open/close/outside-click/Escape behaviour lives here — the
// markup and classes are unchanged, so it still looks like the same menu it
// always did.

import { el } from './dom';

export interface Dropdown {
  /** Positioning context. Append this where the trigger should appear. */
  group: HTMLElement;
  menu: HTMLElement;
  open(): void;
  close(): void;
  toggle(): void;
  readonly isOpen: boolean;
}

export interface DropdownOptions {
  /** Extra class on the positioning wrapper, for alignment overrides. */
  groupClass?: string;
  menuClass?: string;
}

// Only one menu is open at a time; opening a second closes the first, which
// is what stops two overlapping panels when a user clicks straight from one
// trigger to another.
const openMenus = new Set<Dropdown>();

export function dropdown(
  trigger: HTMLElement,
  render: (menu: HTMLElement, close: () => void) => void,
  options: DropdownOptions = {},
): Dropdown {
  const group = el('div', `oa-chip-group${options.groupClass ? ` ${options.groupClass}` : ''}`);
  const menu = el('div', `oa-menu${options.menuClass ? ` ${options.menuClass}` : ''}`);
  menu.hidden = true;

  group.appendChild(trigger);
  group.appendChild(menu);
  trigger.setAttribute('aria-expanded', 'false');
  trigger.setAttribute('aria-haspopup', 'menu');

  // Open is tracked here rather than read off `hidden`, because closing keeps
  // the menu in the document until its transition has run and `hidden` is
  // therefore false for a moment after the menu is, as far as anyone clicking
  // is concerned, shut.
  let open = false;
  let hideTimer = 0;

  // Long enough for the transition in the stylesheet; a few ms over, so the
  // last frame is not cut off.
  const CLOSE_MS = 160;

  const handle: Dropdown = {
    group,
    menu,
    get isOpen() {
      return open;
    },
    open() {
      for (const other of openMenus) if (other !== handle) other.close();
      window.clearTimeout(hideTimer);
      open = true;
      menu.textContent = '';
      render(menu, handle.close);
      menu.hidden = false;
      // One frame closed, so the transition has a state to move from.
      requestAnimationFrame(() => {
        if (open) menu.classList.add('open');
      });
      trigger.setAttribute('aria-expanded', 'true');
      openMenus.add(handle);
    },
    close() {
      if (!open) return;
      open = false;
      menu.classList.remove('open');
      trigger.setAttribute('aria-expanded', 'false');
      openMenus.delete(handle);
      window.clearTimeout(hideTimer);
      hideTimer = window.setTimeout(() => {
        if (open) return;
        menu.hidden = true;
        menu.textContent = '';
      }, CLOSE_MS);
    },
    toggle() {
      if (open) handle.close();
      else handle.open();
    },
  };

  trigger.addEventListener('click', (event) => {
    event.stopPropagation();
    handle.toggle();
  });

  document.addEventListener('click', (event) => {
    if (!handle.isOpen) return;
    if (event.target instanceof Node && group.contains(event.target)) return;
    handle.close();
  });

  document.addEventListener('keydown', (event) => {
    if (event.key === 'Escape') handle.close();
  });

  return handle;
}

/** A row inside a menu: a title, an optional second line, an optional icon. */
export function menuItem(options: {
  title: string;
  sub?: string;
  leading?: Node;
  trailing?: Node;
  active?: boolean;
  onSelect: () => void;
}): HTMLButtonElement {
  const item = el('button', `oa-menu-item${options.active ? ' active' : ''}`);
  item.type = 'button';

  const needsRow = options.leading !== undefined || options.trailing !== undefined;
  const text = el('span', 'oa-menu-item-title', options.title);

  if (needsRow) {
    const row = el('div', 'oa-menu-row');
    if (options.leading) row.appendChild(options.leading);
    row.appendChild(text);
    if (options.trailing) row.appendChild(options.trailing);
    item.appendChild(row);
  } else {
    item.appendChild(text);
  }

  if (options.sub) item.appendChild(el('span', 'oa-menu-item-sub', options.sub));
  item.addEventListener('click', options.onSelect);
  return item;
}

export function menuSeparatorItem(title: string, onSelect: () => void): HTMLButtonElement {
  const item = el('button', 'oa-menu-item oa-menu-manage', title);
  item.type = 'button';
  item.addEventListener('click', onSelect);
  return item;
}
