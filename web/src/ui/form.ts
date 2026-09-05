// Form controls for the administration screens.
//
// Each helper returns the element to place and a typed reader for its value,
// so a form is a list of declarations and a save is a list of reads — rather
// than a pile of `document.querySelector` and `parseInt`.
//
// Everything uses the classes the settings drawer already defined, so an
// admin form is the same material as the one a user sees.

import { el, field } from './dom';
import { t } from '../i18n';

export interface Control<T> {
  element: HTMLElement;
  value(): T;
  set(value: T): void;
  /**
   * Puts the caret here. Pass `{ preventScroll: true }` for a control in a
   * panel that has not slid in yet: the browser would otherwise scroll the
   * whole row sideways to reveal a field that is about to arrive on its own.
   */
  focus(options?: FocusOptions): void;
}

export function textField(options: {
  label: string;
  value?: string;
  placeholder?: string;
  hint?: string;
  type?: string;
  maxLength?: number;
  monospace?: boolean;
}): Control<string> {
  const input = el('input');
  input.type = options.type ?? 'text';
  input.spellcheck = false;
  input.value = options.value ?? '';
  if (options.placeholder) input.placeholder = options.placeholder;
  if (options.maxLength) input.maxLength = options.maxLength;
  if (options.monospace) input.style.fontFamily = 'ui-monospace, SFMono-Regular, Menlo, Consolas, monospace';

  return {
    element: field(options.label, input, options.hint),
    value: () => input.value.trim(),
    set: (value) => { input.value = value; },
    focus: (options) => input.focus(options),
  };
}

export function textArea(options: {
  label: string;
  value?: string;
  placeholder?: string;
  hint?: string;
  rows?: number;
}): Control<string> {
  const area = el('textarea');
  area.rows = options.rows ?? 3;
  area.spellcheck = false;
  area.value = options.value ?? '';
  if (options.placeholder) area.placeholder = options.placeholder;

  return {
    element: field(options.label, area, options.hint),
    value: () => area.value.trim(),
    set: (value) => { area.value = value; },
    focus: (options) => area.focus(options),
  };
}

/**
 * A number that may legitimately be absent. Empty reads as null, which is how
 * "inherit" and "no limit" are expressed — a quota field that fell back to 0
 * would mean "allow nothing".
 */
export function numberField(options: {
  label: string;
  value?: number | null;
  placeholder?: string;
  hint?: string;
  min?: number;
  max?: number;
  step?: number;
}): Control<number | null> {
  const input = el('input');
  input.type = 'number';
  input.value = options.value === null || options.value === undefined ? '' : String(options.value);
  if (options.placeholder) input.placeholder = options.placeholder;
  if (options.min !== undefined) input.min = String(options.min);
  if (options.max !== undefined) input.max = String(options.max);
  if (options.step !== undefined) input.step = String(options.step);

  return {
    element: field(options.label, input, options.hint),
    value: () => {
      const raw = input.value.trim();
      if (raw === '') return null;
      const parsed = Number(raw);
      return Number.isFinite(parsed) ? parsed : null;
    },
    set: (value) => { input.value = value === null ? '' : String(value); },
    focus: (options) => input.focus(options),
  };
}

export function selectField<T extends string>(options: {
  label: string;
  value?: T;
  hint?: string;
  options: Array<{ value: T; label: string }>;
  onChange?(value: T): void;
}): Control<T> {
  const select = el('select');
  for (const entry of options.options) {
    const option = el('option', null, entry.label);
    option.value = entry.value;
    select.appendChild(option);
  }
  if (options.value !== undefined) select.value = options.value;
  if (options.onChange) {
    select.addEventListener('change', () => options.onChange!(select.value as T));
  }

  return {
    element: field(options.label, select, options.hint),
    value: () => select.value as T,
    set: (value) => { select.value = value; },
    focus: (options) => select.focus(options),
  };
}

export function switchField(options: {
  label: string;
  value: boolean;
  hint?: string;
  onChange?(value: boolean): void;
}): Control<boolean> {
  const wrap = el('label', 'oa-checkbox-field');
  const box = el('input');
  box.type = 'checkbox';
  box.checked = options.value;
  if (options.onChange) box.addEventListener('change', () => options.onChange!(box.checked));
  wrap.appendChild(box);
  wrap.appendChild(el('span', null, options.label));

  const element = el('div', 'oa-switch-field');
  element.appendChild(wrap);
  if (options.hint) element.appendChild(el('span', 'oa-field-hint', options.hint));

  return {
    element,
    value: () => box.checked,
    set: (value) => { box.checked = value; },
    focus: (options) => box.focus(options),
  };
}

/** A set of checkboxes — which models a group may use, and nothing else yet. */
export function checkboxList(options: {
  label: string;
  hint?: string;
  items: Array<{ value: string; label: string; sub?: string }>;
  selected: string[];
  emptyText: string;
}): Control<string[]> {
  const list = el('div', 'oa-check-list');
  const boxes = new Map<string, HTMLInputElement>();

  if (!options.items.length) {
    list.appendChild(el('p', 'oa-menu-empty', options.emptyText));
  }
  for (const item of options.items) {
    const row = el('label', 'oa-check-row');
    const box = el('input');
    box.type = 'checkbox';
    box.checked = options.selected.includes(item.value);
    boxes.set(item.value, box);

    const text = el('span', 'oa-check-text');
    text.appendChild(el('span', 'oa-check-title', item.label));
    if (item.sub) text.appendChild(el('span', 'oa-check-sub', item.sub));

    row.appendChild(box);
    row.appendChild(text);
    list.appendChild(row);
  }

  const element = el('div', 'oa-field');
  element.appendChild(el('span', 'oa-field-label', options.label));
  element.appendChild(list);
  if (options.hint) element.appendChild(el('span', 'oa-field-hint', options.hint));

  return {
    element,
    value: () => [...boxes.entries()].filter(([, box]) => box.checked).map(([value]) => value),
    set: (values) => {
      for (const [value, box] of boxes) box.checked = values.includes(value);
    },
    focus: () => {},
  };
}

export type AccessTier = 'use' | 'view' | 'none';

export interface TierListItem {
  value: string;
  label: string;
  sub?: string | undefined;
}

/** A list of items each having a 3-way tier: use, view, or none. */
export function tierList(options: {
  label: string;
  hint?: string;
  items: TierListItem[];
  selected: Record<string, 'use' | 'view'>;
  emptyText: string;
}): Control<Record<string, 'use' | 'view'>> {
  const list = el('div', 'oa-check-list');
  const tiers = new Map<string, AccessTier>();
  const buttonsByItem = new Map<string, Map<AccessTier, HTMLButtonElement>>();

  if (!options.items.length) {
    list.appendChild(el('p', 'oa-menu-empty', options.emptyText));
  }

  const tiersConfig: Array<{ tier: AccessTier; label: string }> = [
    { tier: 'none', label: t('tierNone') },
    { tier: 'view', label: t('tierView') },
    { tier: 'use', label: t('tierUse') },
  ];

  for (const item of options.items) {
    const row = el('div', 'oa-tier-row');
    const text = el('span', 'oa-check-text');
    text.appendChild(el('span', 'oa-check-title', item.label));
    if (item.sub) text.appendChild(el('span', 'oa-check-sub', item.sub));
    row.appendChild(text);

    const initialTier: AccessTier = options.selected[item.value] ?? 'none';
    tiers.set(item.value, initialTier);

    const segmented = el('div', 'oa-segmented');
    const itemButtons = new Map<AccessTier, HTMLButtonElement>();

    for (const { tier, label } of tiersConfig) {
      const btn = el('button', `oa-segmented-option${tier === initialTier ? ' active' : ''}`, label);
      btn.type = 'button';
      btn.addEventListener('click', () => {
        tiers.set(item.value, tier);
        for (const [tKey, b] of itemButtons) {
          b.classList.toggle('active', tKey === tier);
        }
      });
      itemButtons.set(tier, btn);
      segmented.appendChild(btn);
    }

    buttonsByItem.set(item.value, itemButtons);
    row.appendChild(segmented);
    list.appendChild(row);
  }

  const element = el('div', 'oa-field');
  element.appendChild(el('span', 'oa-field-label', options.label));
  element.appendChild(list);
  if (options.hint) element.appendChild(el('span', 'oa-field-hint', options.hint));

  return {
    element,
    value: () => {
      const out: Record<string, 'use' | 'view'> = {};
      for (const [id, tier] of tiers) {
        if (tier === 'use' || tier === 'view') {
          out[id] = tier;
        }
      }
      return out;
    },
    set: (values) => {
      for (const item of options.items) {
        const nextTier: AccessTier = values[item.value] ?? 'none';
        tiers.set(item.value, nextTier);
        const itemButtons = buttonsByItem.get(item.value);
        if (itemButtons) {
          for (const [tKey, b] of itemButtons) {
            b.classList.toggle('active', tKey === nextTier);
          }
        }
      }
    },
    focus: () => {},
  };
}

/** A labelled divider between groups of fields inside one form. */
export function section(title: string, hint?: string): HTMLElement {
  const wrap = el('div', 'oa-form-section');
  wrap.appendChild(el('h3', 'oa-drawer-subhead', title));
  if (hint) wrap.appendChild(el('p', 'oa-field-hint', hint));
  return wrap;
}
