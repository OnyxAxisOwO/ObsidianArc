// The option list, drawn by this stylesheet instead of by the operating
// system.
//
// A native <select> hands its popup to the platform. That is why every
// dropdown here used to open a grey OS list with a blue bar through it, in
// the middle of a rounded translucent panel. The stylesheet did try: there
// was a `appearance: base-select` block that restyles the native picker. It
// sits behind @supports and Chromium is the only engine that has it, so on
// anything else the whole block was skipped and the OS list came back — the
// feature looked finished to whoever had the right browser and had never
// shipped for anybody else.
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
// Long enough for the transition in the stylesheet.
const CLOSE_MS = 160;

// aria-activedescendant needs an id to point at, and two selects on one
// screen must not mint the same one.
let sequence = 0;

/**
 * The list that is open, if any. Only one can be: opening a second closes the
 * first, which is what stops two lists overlapping when somebody clicks
 * straight from one control to another.
 */
let openList: { close(): void } | null = null;

/**
 * One dismisser for every select there will ever be, rather than one per
 * control. The router's registry is a Set with no removal, so registering per
 * instance would leave an entry behind for every control ever built — and the
 * bug that registry exists to prevent is exactly this kind of leftover.
 */
onBeforeRender(() => openList?.close());

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
    // block: 'nearest' so opening a list whose selection is already in view
    // does not jump it to the middle.
    row.scrollIntoView({ block: 'nearest' });
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
      row.addEventListener('click', () => {
        commit(choice.value);
        close();
      });
      list.appendChild(row);
    });
  }

  function place(): void {
    const box = trigger.getBoundingClientRect();
    // Never narrower than the control, so the list reads as belonging to it.
    list.style.minWidth = `${box.width}px`;
    list.style.left = '0px';
    list.style.top = '0px';

    const height = list.offsetHeight;
    const width = list.offsetWidth;
    const below = window.innerHeight - box.bottom - GAP;
    const above = box.top - GAP;
    // Above only when there is genuinely more room there: a list that flips
    // for the sake of a few pixels is worse than one that scrolls.
    const flip = below < height && above > below;

    list.style.top = flip
      ? `${Math.max(MARGIN, box.top - GAP - height)}px`
      : `${box.bottom + GAP}px`;
    list.style.transformOrigin = flip ? 'bottom left' : 'top left';
    list.style.left = `${Math.max(MARGIN, Math.min(box.left, window.innerWidth - MARGIN - width))}px`;
  }

  function commit(value: T): void {
    if (value === current) return;
    current = value;
    paintLabel();
    config.onChange?.(current);
  }

  // Listeners live only while the list is open. menu.ts adds its outside-click
  // and Escape handlers to `document` once per dropdown and never removes
  // them; with a control this common that is a handler per control per render.
  function watch(on: boolean): void {
    const method = on ? 'addEventListener' : 'removeEventListener';
    document[method]('pointerdown', outside, true);
    // Capture, so the panel's own scroller is heard and not just the window.
    window[method]('scroll', reposition, true);
    window[method]('resize', reposition);
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

  function reposition(): void {
    if (!open) return;
    const box = trigger.getBoundingClientRect();
    // Scrolled out of the window entirely — usually because the panel behind
    // it scrolled. A list still hanging in the middle of the screen with
    // nothing to belong to is worse than one that shuts.
    if (box.bottom < 0 || box.top > window.innerHeight) {
      close();
      return;
    }
    place();
  }

  function openMenu(): void {
    if (open) return;
    openList?.close();
    window.clearTimeout(hideTimer);
    open = true;
    openList = { close };

    build();
    list.hidden = false;
    document.body.appendChild(list);
    place();
    trigger.setAttribute('aria-expanded', 'true');
    trigger.setAttribute('aria-controls', id);
    markActive(Math.max(0, choices.findIndex((choice) => choice.value === current)));
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

  /** The first choice after `from` whose label starts with what was typed. */
  function search(): void {
    const at = choices.findIndex((choice, index) =>
      index > (typed.length > 1 ? active - 1 : active)
      && choice.label.toLowerCase().startsWith(typed));
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
        const step = event.key === 'ArrowDown' ? 1 : -1;
        if (!open) {
          openMenu();
          return;
        }
        markActive(active + step);
        return;
      }
      case 'Home':
      case 'End':
        if (!open) return;
        event.preventDefault();
        markActive(event.key === 'Home' ? 0 : choices.length - 1);
        return;
      case 'Enter':
      case ' ':
        event.preventDefault();
        if (!open) {
          openMenu();
          return;
        }
        commit(choices[active]!.value);
        close();
        return;
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
    // alone. Date.now is fine here: this is the interface, not a workflow.
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
