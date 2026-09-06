// The option list, drawn here instead of by the operating system.
//
// A native <select> hands its popup to the platform. That is why every
// dropdown here used to open a grey OS list with a blue bar through it, in
// the middle of a rounded translucent panel. The stylesheet did try: there
// was an `appearance: base-select` block restyling the native picker. Chromium
// is the only engine that has that property, and it sat behind @supports — so
// the work looked finished to anyone with the right browser and had never
// once shipped for anybody else.
//
// What makes this harder than the menu in menu.ts: a select appears inside
// something that clips, every time. The settings body and the admin body
// scroll, .oa-panel and .oa-admin-main both carry a backdrop-filter, and
// .oa-settings keeps an identity transform left behind by its entry
// animation. A transform, a filter or a backdrop-filter makes an element the
// containing block for `position: fixed`, so a popup left inside the field is
// trapped by an ancestor whether it is absolute or fixed. The list is
// therefore appended to <body>, out of every one of them, and placed from the
// trigger's rectangle.
//
// That portal is also the cost: a node on <body> has no idea its trigger has
// been thrown away. Screens here are torn down in four ways that are not a
// router render — panel.ts empties its body, the admin rail swaps its whole
// body node, settings replaces a pane, the log filters clear their row — and
// none of them announce it. So while a list is open one frame callback
// watches the trigger: it closes when the trigger leaves the document or the
// window, and re-places when it moves.

import { el } from './dom';
import { onBeforeRender } from '../router';

export interface Choice<T extends string> {
  value: T;
  label: string;
}

export interface SelectControl<T extends string> {
  /** The trigger. Place this where a <select> would have gone. */
  element: HTMLButtonElement;
  value(): T;
  set(value: T): void;
  /** Replaces the list. Keeps the current value if it is still in it. */
  setChoices(choices: Array<Choice<T>>): void;
  focus(options?: FocusOptions): void;
}

// The gap between the control and its list, matching the one .oa-menu leaves.
const GAP = 6;
// Room kept between the list and the edge of the window.
const MARGIN = 8;
// The tallest a list gets before it scrolls, matching .oa-menu.
const MAX_HEIGHT = 320;
// Long enough for the transition in the stylesheet.
const CLOSE_MS = 160;

// aria-activedescendant needs an id to point at, and two selects on one
// screen must not mint the same one.
let sequence = 0;

/**
 * The list that is open, if any. Only one can be: opening a second closes the
 * first, which is what stops two lists overlapping when somebody clicks
 * straight from one trigger to another.
 */
let openList: { close(): void } | null = null;

/**
 * One dismisser for every select there will ever be, rather than one per
 * control. The router's registry is a Set with no removal, so registering per
 * instance would leave an entry behind for every control ever built — and the
 * bug that registry exists to prevent is exactly this kind of leftover.
 */
onBeforeRender(() => openList?.close());

export interface TriggerBox {
  top: number;
  bottom: number;
  left: number;
  width: number;
}

export interface Placement {
  top: number;
  left: number;
  maxHeight: number;
  origin: 'top left' | 'bottom left';
}

/**
 * Where the list goes, given the trigger's rectangle and the height the list
 * would like to be.
 *
 * Pure, and exported, because it is the part with the arithmetic in it and
 * jsdom reports every rectangle as zero — a browser is needed to see this
 * happen but not to check that it is right.
 *
 * The clamp is the point. A list is capped at the room on the side it ends up
 * on, and the flip is then decided against those clamped heights: a static
 * cap would let a 320px list hang off the bottom of a short window, where a
 * body-fixed node scrolls with nothing and the rows below the fold are simply
 * unreachable.
 */
export function placeList(
  box: TriggerBox,
  natural: number,
  width: number,
  view: { width: number; height: number },
): Placement {
  const below = Math.max(0, view.height - box.bottom - GAP - MARGIN);
  const above = Math.max(0, box.top - GAP - MARGIN);
  // Above only when it buys room. Both sides clamp, so the question is which
  // one fits more of the list, not which one fits all of it.
  const flip = natural > below && above > below;
  const room = Math.min(MAX_HEIGHT, flip ? above : below);
  const height = Math.min(natural, room);

  return {
    top: flip ? Math.max(MARGIN, box.top - GAP - height) : box.bottom + GAP,
    left: Math.max(MARGIN, Math.min(box.left, view.width - MARGIN - width)),
    maxHeight: room,
    origin: flip ? 'bottom left' : 'top left',
  };
}

export function select<T extends string>(config: {
  choices: Array<Choice<T>>;
  value?: T;
  /** Extra classes on the trigger, for the filter bars. */
  className?: string;
  /** For a control with no <label> around it. */
  ariaLabel?: string;
  onChange?(value: T): void;
}): SelectControl<T> {
  let choices = config.choices;
  let current = (config.value ?? choices[0]?.value ?? '') as T;

  const id = `oa-select-${(sequence += 1)}`;
  const trigger = el('button', `oa-select${config.className ? ` ${config.className}` : ''}`);
  trigger.type = 'button';
  trigger.setAttribute('role', 'combobox');
  trigger.setAttribute('aria-haspopup', 'listbox');
  trigger.setAttribute('aria-expanded', 'false');
  if (config.ariaLabel) trigger.setAttribute('aria-label', config.ariaLabel);
  const label = el('span', 'oa-select-label');
  trigger.appendChild(label);

  const list = el('div', 'oa-menu oa-select-menu');
  list.id = id;
  list.setAttribute('role', 'listbox');
  list.hidden = true;

  let open = false;
  let active = 0;
  let hideTimer = 0;
  let frame = 0;
  // The list's height and width with nothing capping them, measured once per
  // build rather than once per frame: reading either forces a layout of the
  // whole list, and the watch below runs sixty times a second.
  let natural = 0;
  let width = 0;
  let anchor = '';
  // Type-ahead: the letters typed so far and when the last one arrived, so a
  // pause starts a new word the way a native select does.
  let typed = '';
  let typedAt = 0;

  function paintLabel(): void {
    label.textContent = choices.find((choice) => choice.value === current)?.label ?? '';
  }

  function rows(): HTMLElement[] {
    return Array.from(list.children) as HTMLElement[];
  }

  function markActive(index: number): void {
    const items = rows();
    if (!items.length) return;
    active = Math.max(0, Math.min(index, items.length - 1));
    items.forEach((row, i) => row.classList.toggle('active', i === active));
    const row = items[active]!;
    trigger.setAttribute('aria-activedescendant', row.id);

    // The list's own scrollTop rather than scrollIntoView: the list is a
    // child of <body>, and scrollIntoView on one of those is entitled to
    // scroll the document under the reader to reveal it.
    const top = row.offsetTop;
    const bottom = top + row.offsetHeight;
    if (top < list.scrollTop) list.scrollTop = top;
    else if (bottom > list.scrollTop + list.clientHeight) list.scrollTop = bottom - list.clientHeight;
  }

  function build(): void {
    list.textContent = '';
    choices.forEach((choice, index) => {
      // A div, not a button: focus stays on the trigger and the active row is
      // named by aria-activedescendant, which is the combobox pattern. A
      // button here would take focus on mousedown and the trigger would lose
      // the keydown handler mid-interaction.
      const row = el('div', 'oa-menu-item');
      row.id = `${id}-${index}`;
      row.setAttribute('role', 'option');
      row.setAttribute('aria-selected', String(choice.value === current));
      row.appendChild(el('span', 'oa-menu-item-title', choice.label));
      // mousedown rather than click, and prevented, so the press does not
      // move focus off the trigger before the click lands.
      row.addEventListener('mousedown', (event) => event.preventDefault());
      row.addEventListener('click', () => choose(choice.value));
      list.appendChild(row);
    });
    // A rebuilt list has a new height, a new set of ids, and possibly fewer
    // rows than the arrow keys had walked to.
    measure();
    place();
    markActive(Math.max(0, choices.findIndex((choice) => choice.value === current)));
  }

  function measure(): void {
    list.style.maxHeight = '';
    list.style.top = '0px';
    list.style.left = '0px';
    natural = list.offsetHeight;
    width = list.offsetWidth;
  }

  function place(): void {
    const box = trigger.getBoundingClientRect();
    // Never narrower than the control, so the list reads as belonging to it.
    list.style.minWidth = `${box.width}px`;
    const spot = placeList(box, natural, width, {
      width: window.innerWidth,
      height: window.innerHeight,
    });
    list.style.top = `${spot.top}px`;
    list.style.left = `${spot.left}px`;
    list.style.maxHeight = `${spot.maxHeight}px`;
    list.style.transformOrigin = spot.origin;
  }

  function commit(value: T): void {
    if (value === current) return;
    current = value;
    paintLabel();
    config.onChange?.(current);
  }

  /**
   * Shut first, then report. Two live callers rebuild the panel they are in
   * from onChange — admin/providers.ts and admin/usage.ts — which destroys
   * this trigger underneath an open list. Closing first means the listeners
   * are already off and the node already going when that happens, rather than
   * it working by accident because remove() tolerates a detached parent.
   */
  function choose(value: T): void {
    close();
    commit(value);
  }

  // Listeners live only while the list is open. menu.ts adds its outside-click
  // and Escape handlers to `document` once per dropdown and never removes
  // them; with a control this common that is a handler per control per render.
  function watch(on: boolean): void {
    if (on) {
      document.addEventListener('pointerdown', outside, true);
      anchor = '';
      frame = requestAnimationFrame(follow);
      return;
    }
    document.removeEventListener('pointerdown', outside, true);
    cancelAnimationFrame(frame);
    frame = 0;
  }

  /**
   * One read a frame, and a write only when the trigger has actually moved.
   *
   * This is scroll, resize, an animating panel and a torn-down screen in one
   * mechanism, rather than a listener for each and nothing at all for the
   * last — which is the case that leaves a list on <body> over an unrelated
   * screen with its handlers still attached.
   */
  function follow(): void {
    if (!open) return;
    frame = requestAnimationFrame(follow);

    if (!trigger.isConnected) {
      close();
      return;
    }
    const box = trigger.getBoundingClientRect();
    // Scrolled out of the window, usually because the panel behind it
    // scrolled. A list hanging in the middle of the screen with nothing to
    // belong to is worse than one that shuts.
    if (box.bottom < 0 || box.top > window.innerHeight) {
      close();
      return;
    }

    const key = `${box.top}|${box.left}|${box.width}`;
    if (key === anchor) return;
    // A trigger that changed width changes the list's width with it, which is
    // the one move that needs the size measured again.
    if (anchor.split('|')[2] !== String(box.width)) measure();
    anchor = key;
    place();
  }

  function outside(event: Event): void {
    const target = event.target;
    if (!(target instanceof Node)) return;
    if (list.contains(target)) return;
    // The trigger sits inside a <label>, which forwards a click on its text to
    // the control. Treating that text as outside would close the list in the
    // same gesture that opened it.
    if (trigger.closest('label')?.contains(target) ?? trigger.contains(target)) return;
    close();
  }

  function openMenu(): void {
    if (open) return;
    openList?.close();
    window.clearTimeout(hideTimer);
    open = true;
    openList = { close };

    list.hidden = false;
    document.body.appendChild(list);
    // Before build(), which measures: a hidden node has no height.
    build();
    trigger.setAttribute('aria-expanded', 'true');
    trigger.setAttribute('aria-controls', id);
    // WebKit does not focus a button on click, and every key this control
    // reads arrives on the trigger. Without this the arrows, Enter and Escape
    // are all inert there for a list opened with the mouse — and Escape falls
    // through to the panel, which closes underneath the open list.
    trigger.focus({ preventScroll: true });
    watch(true);
    // One frame closed, so the transition has a state to move from.
    requestAnimationFrame(() => {
      if (open) list.classList.add('open');
    });
  }

  function close(): void {
    if (!open) return;
    open = false;
    if (openList?.close === close) openList = null;
    watch(false);
    list.classList.remove('open');
    trigger.setAttribute('aria-expanded', 'false');
    trigger.removeAttribute('aria-activedescendant');
    typed = '';
    window.clearTimeout(hideTimer);
    hideTimer = window.setTimeout(() => {
      if (open) return;
      list.hidden = true;
      list.remove();
    }, CLOSE_MS);
  }

  /** The first choice after the active one whose label starts with the typing. */
  function search(): void {
    const from = typed.length > 1 ? active - 1 : active;
    const at = choices.findIndex((choice, index) =>
      index > from && choice.label.toLowerCase().startsWith(typed));
    const found = at >= 0
      ? at
      : choices.findIndex((choice) => choice.label.toLowerCase().startsWith(typed));
    if (found < 0) return;
    if (open) markActive(found);
    else commit(choices[found]!.value);
  }

  trigger.addEventListener('click', () => {
    if (open) close();
    else openMenu();
  });

  trigger.addEventListener('keydown', (event) => {
    switch (event.key) {
      case 'ArrowDown':
      case 'ArrowUp': {
        event.preventDefault();
        if (!open) {
          openMenu();
          return;
        }
        markActive(active + (event.key === 'ArrowDown' ? 1 : -1));
        return;
      }
      case 'Home':
      case 'End':
        if (!open) return;
        event.preventDefault();
        markActive(event.key === 'Home' ? 0 : choices.length - 1);
        return;
      case 'Enter':
      case ' ': {
        event.preventDefault();
        if (!open) {
          openMenu();
          return;
        }
        // The list can have been rebuilt from under the walk — the default
        // model picker fills itself from a request — so there may be nothing
        // at this index. Throwing here would leave the list open with its
        // listeners attached, which is the leak this file is careful about.
        const picked = choices[active];
        if (picked) choose(picked.value);
        else close();
        return;
      }
      case 'Escape':
        if (!open) return;
        event.preventDefault();
        close();
        return;
      case 'Tab':
        close();
        return;
      default:
        break;
    }

    // A single printable character, so a shortcut with a modifier is left
    // alone.
    if (event.key.length !== 1 || event.ctrlKey || event.metaKey || event.altKey) return;
    const now = Date.now();
    typed = now - typedAt > 900 ? event.key.toLowerCase() : typed + event.key.toLowerCase();
    typedAt = now;
    search();
  });

  paintLabel();

  return {
    element: trigger,
    value: () => current,
    set: (value) => {
      current = value;
      paintLabel();
      if (open) build();
    },
    setChoices: (next) => {
      choices = next;
      if (!next.some((choice) => choice.value === current)) {
        current = (next[0]?.value ?? '') as T;
      }
      paintLabel();
      if (open) build();
    },
    focus: (options) => trigger.focus(options),
  };
}
