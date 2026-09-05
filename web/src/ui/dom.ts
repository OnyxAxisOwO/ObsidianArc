// The element helpers the whole interface is built from.
//
// The standalone build defined these twice, once in the chat module and once
// in the workspace shell. They are one module now, which is what lets the
// login page and the admin backoffice be drawn from the same vocabulary as
// the chat — same radii, same hover tint, same icon weight — without any of
// them importing each other.
//
// Everything here builds nodes. Nothing in this project assigns innerHTML:
// the transcript renders model output, and a single string-building path
// anywhere is the hole that makes the rest of the care pointless.

const SVG_NS = 'http://www.w3.org/2000/svg';

export function el<K extends keyof HTMLElementTagNameMap>(
  tag: K,
  className?: string | null,
  text?: string | null,
): HTMLElementTagNameMap[K] {
  const node = document.createElement(tag);
  if (className) node.className = className;
  if (text != null) node.textContent = text;
  return node;
}

export function button(
  className: string | null,
  text?: string | null,
  onClick?: (event: MouseEvent) => void,
): HTMLButtonElement {
  const node = el('button', className, text);
  node.type = 'button';
  if (onClick) node.addEventListener('click', onClick);
  return node;
}

// Stroked 24×24 line icons, drawn to match the set the standalone build used.
export function icon(paths: readonly string[], size = 16): SVGSVGElement {
  const svg = document.createElementNS(SVG_NS, 'svg');
  svg.setAttribute('viewBox', '0 0 24 24');
  svg.setAttribute('width', String(size));
  svg.setAttribute('height', String(size));
  svg.setAttribute('fill', 'none');
  svg.setAttribute('stroke', 'currentColor');
  svg.setAttribute('stroke-width', '2');
  svg.setAttribute('stroke-linecap', 'round');
  svg.setAttribute('stroke-linejoin', 'round');
  svg.setAttribute('aria-hidden', 'true');
  for (const d of paths) {
    const path = document.createElementNS(SVG_NS, 'path');
    path.setAttribute('d', d);
    svg.appendChild(path);
  }
  return svg;
}

// An icon-only control needs its name somewhere a screen reader and a
// hovering pointer can both find it.
export function labelled<T extends HTMLElement>(node: T, label: string): T {
  node.title = label;
  node.setAttribute('aria-label', label);
  return node;
}

export function iconButton(
  className: string,
  paths: readonly string[],
  label: string,
  onClick?: (event: MouseEvent) => void,
  size = 16,
): HTMLButtonElement {
  const node = button(className, '', onClick);
  node.appendChild(icon(paths, size));
  return labelled(node, label);
}

export function clear(node: Element): void {
  node.textContent = '';
}

export function field(labelText: string, control: Node, hint?: string): HTMLLabelElement {
  const wrap = el('label', 'oa-field');
  wrap.appendChild(el('span', 'oa-field-label', labelText));
  wrap.appendChild(control);
  if (hint) wrap.appendChild(el('span', 'oa-field-hint', hint));
  return wrap;
}

export interface CheckboxField {
  wrap: HTMLLabelElement;
  box: HTMLInputElement;
}

export function checkboxField(
  labelText: string,
  checked: boolean,
  onChange: (checked: boolean) => void,
): CheckboxField {
  const wrap = el('label', 'oa-checkbox-field');
  const box = el('input');
  box.type = 'checkbox';
  box.checked = checked;
  box.addEventListener('change', () => onChange(box.checked));
  wrap.appendChild(box);
  wrap.appendChild(el('span', null, labelText));
  return { wrap, box };
}

export function textInput(options: {
  type?: string;
  placeholder?: string;
  value?: string;
  autocomplete?: AutoFill;
  maxLength?: number;
}): HTMLInputElement {
  const input = el('input');
  input.type = options.type ?? 'text';
  input.spellcheck = false;
  if (options.placeholder) input.placeholder = options.placeholder;
  if (options.value) input.value = options.value;
  if (options.autocomplete) input.autocomplete = options.autocomplete;
  if (options.maxLength) input.maxLength = options.maxLength;
  return input;
}

export const ICONS = {
  menu: ['M3 6h18', 'M3 12h18', 'M3 18h18'],
  plus: ['M12 5v14', 'M5 12h14'],
  close: ['M18 6L6 18', 'M6 6l12 12'],
  check: ['M20 6 9 17l-5-5'],
  chevron: ['m6 9 6 6 6-6'],
  chevronRight: ['m9 6 6 6-6 6'],
  trash: ['M3 6h18', 'M8 6V4h8v2', 'M19 6l-1 14H6L5 6', 'M10 11v6', 'M14 11v6'],
  paperclip: [
    'M21.44 11.05l-9.19 9.19a6 6 0 0 1-8.49-8.49l9.19-9.19a4 4 0 0 1 5.66 5.66l-9.2 9.19a2 2 0 0 1-2.83-2.83l8.49-8.48',
  ],
  send: ['M12 19V5', 'M5 12l7-7 7 7'],
  stop: ['M7 7h10v10H7z'],
  sun: [
    'M12 17a5 5 0 1 0 0-10 5 5 0 0 0 0 10Z', 'M12 1v2', 'M12 21v2',
    'M4.22 4.22l1.42 1.42', 'M18.36 18.36l1.42 1.42', 'M1 12h2', 'M21 12h2',
    'M4.22 19.78l1.42-1.42', 'M18.36 5.64l1.42-1.42',
  ],
  moon: ['M21 12.79A9 9 0 1 1 11.21 3 7 7 0 0 0 21 12.79Z'],
  auto: ['M3 5h18v11H3z', 'M8 20h8', 'M12 16v4'],
  gear: [
    'M12 15a3 3 0 1 0 0-6 3 3 0 0 0 0 6Z',
    'M19.4 15a1.65 1.65 0 0 0 .33 1.82l.06.06a2 2 0 1 1-2.83 2.83l-.06-.06a1.65 1.65 0 0 0-1.82-.33 1.65 1.65 0 0 0-1 1.51V21a2 2 0 0 1-4 0v-.09a1.65 1.65 0 0 0-1.08-1.51 1.65 1.65 0 0 0-1.82.33l-.06.06a2 2 0 1 1-2.83-2.83l.06-.06a1.65 1.65 0 0 0 .33-1.82 1.65 1.65 0 0 0-1.51-1H3a2 2 0 0 1 0-4h.09a1.65 1.65 0 0 0 1.51-1.08 1.65 1.65 0 0 0-.33-1.82l-.06-.06a2 2 0 1 1 2.83-2.83l.06.06a1.65 1.65 0 0 0 1.82.33H9a1.65 1.65 0 0 0 1-1.51V3a2 2 0 0 1 4 0v.09a1.65 1.65 0 0 0 1 1.51 1.65 1.65 0 0 0 1.82-.33l.06-.06a2 2 0 1 1 2.83 2.83l-.06.06a1.65 1.65 0 0 0-.33 1.82V9a1.65 1.65 0 0 0 1.51 1H21a2 2 0 0 1 0 4h-.09a1.65 1.65 0 0 0-1.51 1Z',
  ],
  eye: ['M1 12s4-7 11-7 11 7 11 7-4 7-11 7-11-7-11-7Z', 'M12 15a3 3 0 1 0 0-6 3 3 0 0 0 0 6Z'],
  eyeOff: [
    'M17.94 17.94A10.94 10.94 0 0 1 12 19c-7 0-11-7-11-7a18.4 18.4 0 0 1 4.22-5.06',
    'M9.9 4.24A9.12 9.12 0 0 1 12 4c7 0 11 7 11 7a18.5 18.5 0 0 1-2.16 3.19',
    'M14.12 14.12a3 3 0 1 1-4.24-4.24', 'M1 1l22 22',
  ],
  user: ['M20 21v-2a4 4 0 0 0-4-4H8a4 4 0 0 0-4 4v2', 'M12 11a4 4 0 1 0 0-8 4 4 0 0 0 0 8Z'],
  users: [
    'M17 21v-2a4 4 0 0 0-4-4H5a4 4 0 0 0-4 4v2', 'M9 11a4 4 0 1 0 0-8 4 4 0 0 0 0 8Z',
    'M23 21v-2a4 4 0 0 0-3-3.87', 'M16 3.13a4 4 0 0 1 0 7.75',
  ],
  layers: ['M12 2 2 7l10 5 10-5-10-5Z', 'M2 17l10 5 10-5', 'M2 12l10 5 10-5'],
  server: [
    'M2 3h20v6H2z', 'M2 15h20v6H2z', 'M6 6h.01', 'M6 18h.01',
  ],
  chart: ['M3 3v18h18', 'M7 15l4-4 3 3 5-6'],
  sliders: ['M4 21v-7', 'M4 10V3', 'M12 21v-9', 'M12 8V3', 'M20 21v-5', 'M20 12V3', 'M1 14h6', 'M9 8h6', 'M17 16h6'],
  logout: ['M9 21H5a2 2 0 0 1-2-2V5a2 2 0 0 1 2-2h4', 'M16 17l5-5-5-5', 'M21 12H9'],
  home: ['M3 10.5 12 3l9 7.5', 'M5 9.5V21h14V9.5'],
  spark: ['M12 3v4', 'M12 17v4', 'M3 12h4', 'M17 12h4', 'M5.6 5.6l2.8 2.8', 'M15.6 15.6l2.8 2.8', 'M18.4 5.6l-2.8 2.8', 'M8.4 15.6l-2.8 2.8'],
} as const;
