// Bar and pie charts, drawn as SVG by hand.
//
// No library, because a charting dependency is several hundred kilobytes and
// its own theming system for two shapes this project needs, and because
// everything here has to answer to the accent: a chart drawn in someone
// else's palette is the one part of the interface that ignores the colour the
// user picked.
//
// Both take the same data, so a toggle between them is a re-render rather
// than a different code path — which is the whole point of offering the
// choice. Neither animates on data change: these are read, not watched.

import { el } from './dom';

export interface Slice {
  key: string;
  label: string;
  value: number;
  /** Shown under the label, e.g. the request count behind a credit figure. */
  note?: string;
}

export type ChartShape = 'bar' | 'pie';

export interface ChartOptions {
  shape: ChartShape;
  data: Slice[];
  /** Renders each value for the axis and the legend. */
  format(value: number): string;
  emptyText: string;
  /** Slices past this are folded into one "other" entry. */
  max?: number;
  otherLabel?: string;
  onSelect?(key: string): void;
}

const SVG_NS = 'http://www.w3.org/2000/svg';

/**
 * The palette a chart draws with.
 *
 * Derived from the accent rather than fixed, by walking lightness across a
 * single hue. That keeps a chart legible on both schemes, keeps it in the
 * user's colour, and degenerates to a grey ramp when the accent is neutral —
 * which is this instance's default and looks deliberate rather than broken.
 */
function palette(count: number): string[] {
  const root = getComputedStyle(document.documentElement);
  const accent = root.getPropertyValue('--ai-primary').trim() || '#71717a';
  const dark = document.documentElement.getAttribute('data-theme') === 'dark'
    || (!document.documentElement.getAttribute('data-theme')
      && window.matchMedia('(prefers-color-scheme: dark)').matches);

  const [hue, saturation] = hueOf(accent);
  const out: string[] = [];
  for (let i = 0; i < Math.max(1, count); i += 1) {
    // Spread across a band rather than the whole range: the extremes are
    // invisible against one background or the other.
    const step = count > 1 ? i / (count - 1) : 0;
    const light = dark ? 74 - step * 34 : 34 + step * 38;
    // Fade saturation along the ramp so the first slice is the emphatic one.
    const sat = Math.max(0, saturation * (1 - step * 0.45));
    out.push(`hsl(${hue} ${sat.toFixed(0)}% ${light.toFixed(0)}%)`);
  }
  return out;
}

/** Reads a hex or hsl accent into a hue and saturation. */
function hueOf(colour: string): [number, number] {
  const hsl = colour.match(/hsl\(\s*([\d.]+)\s*,?\s*([\d.]+)%/i);
  if (hsl) return [Number(hsl[1]), Number(hsl[2])];

  const hex = colour.replace('#', '');
  if (hex.length !== 3 && hex.length !== 6) return [240, 4];
  const full = hex.length === 3 ? hex.split('').map((c) => c + c).join('') : hex;
  const r = parseInt(full.slice(0, 2), 16) / 255;
  const g = parseInt(full.slice(2, 4), 16) / 255;
  const b = parseInt(full.slice(4, 6), 16) / 255;

  const maximum = Math.max(r, g, b);
  const minimum = Math.min(r, g, b);
  const delta = maximum - minimum;
  if (delta === 0) return [240, 4];

  let hue = 0;
  if (maximum === r) hue = ((g - b) / delta) % 6;
  else if (maximum === g) hue = (b - r) / delta + 2;
  else hue = (r - g) / delta + 4;
  hue = Math.round(hue * 60);
  if (hue < 0) hue += 360;

  const lightness = (maximum + minimum) / 2;
  const saturation = delta / (1 - Math.abs(2 * lightness - 1));
  // Held above a floor so a near-black accent still produces distinguishable
  // slices rather than five identical greys.
  return [hue, Math.max(12, Math.min(70, saturation * 100))];
}

export function renderChart(options: ChartOptions): HTMLElement {
  const wrap = el('div', 'oa-chart');
  const data = fold(options);

  if (!data.length || data.every((slice) => slice.value <= 0)) {
    wrap.appendChild(el('p', 'oa-menu-empty', options.emptyText));
    return wrap;
  }

  const colours = palette(data.length);
  wrap.appendChild(options.shape === 'pie'
    ? pie(data, colours, options)
    : bars(data, colours, options));
  wrap.appendChild(legend(data, colours, options));
  return wrap;
}

/** Folds the tail into one slice, so a pie of forty models stays readable. */
function fold(options: ChartOptions): Slice[] {
  const sorted = [...options.data].filter((slice) => slice.value > 0)
    .sort((a, b) => b.value - a.value);
  const max = options.max ?? 8;
  if (sorted.length <= max) return sorted;

  const head = sorted.slice(0, max - 1);
  const tail = sorted.slice(max - 1);
  head.push({
    key: '',
    label: options.otherLabel ?? 'Other',
    value: tail.reduce((sum, slice) => sum + slice.value, 0),
    note: String(tail.length),
  });
  return head;
}

function svg(width: number, height: number): SVGSVGElement {
  const node = document.createElementNS(SVG_NS, 'svg');
  node.setAttribute('viewBox', `0 0 ${width} ${height}`);
  node.setAttribute('class', 'oa-chart-svg');
  node.setAttribute('role', 'img');
  return node;
}

function bars(data: Slice[], colours: string[], options: ChartOptions): SVGSVGElement {
  const width = 320;
  const rowHeight = 26;
  const gap = 6;
  const height = data.length * (rowHeight + gap);
  const node = svg(width, height);

  const largest = Math.max(...data.map((slice) => slice.value));
  data.forEach((slice, index) => {
    const y = index * (rowHeight + gap);
    const length = largest > 0 ? Math.max(2, (slice.value / largest) * width) : 0;

    const rect = document.createElementNS(SVG_NS, 'rect');
    rect.setAttribute('x', '0');
    rect.setAttribute('y', String(y));
    rect.setAttribute('width', String(length));
    rect.setAttribute('height', String(rowHeight));
    rect.setAttribute('rx', '6');
    rect.setAttribute('fill', colours[index]!);
    if (options.onSelect && slice.key) {
      rect.setAttribute('class', 'oa-chart-hit');
      rect.addEventListener('click', () => options.onSelect!(slice.key));
    }

    const title = document.createElementNS(SVG_NS, 'title');
    title.textContent = `${slice.label} — ${options.format(slice.value)}`;
    rect.appendChild(title);
    node.appendChild(rect);
  });
  return node;
}

function pie(data: Slice[], colours: string[], options: ChartOptions): SVGSVGElement {
  const size = 200;
  const radius = 92;
  const centre = size / 2;
  const node = svg(size, size);

  const total = data.reduce((sum, slice) => sum + slice.value, 0);
  if (total <= 0) return node;

  // A single slice is a whole circle, which an arc path cannot express: the
  // start and end points coincide and the browser draws nothing.
  if (data.length === 1) {
    const circle = document.createElementNS(SVG_NS, 'circle');
    circle.setAttribute('cx', String(centre));
    circle.setAttribute('cy', String(centre));
    circle.setAttribute('r', String(radius));
    circle.setAttribute('fill', colours[0]!);
    node.appendChild(circle);
    return node;
  }

  let angle = -Math.PI / 2; // Start at twelve o'clock.
  data.forEach((slice, index) => {
    const sweep = (slice.value / total) * Math.PI * 2;
    const end = angle + sweep;

    const path = document.createElementNS(SVG_NS, 'path');
    path.setAttribute('d', [
      `M ${centre} ${centre}`,
      `L ${(centre + radius * Math.cos(angle)).toFixed(2)} ${(centre + radius * Math.sin(angle)).toFixed(2)}`,
      `A ${radius} ${radius} 0 ${sweep > Math.PI ? 1 : 0} 1`,
      `${(centre + radius * Math.cos(end)).toFixed(2)} ${(centre + radius * Math.sin(end)).toFixed(2)}`,
      'Z',
    ].join(' '));
    path.setAttribute('fill', colours[index]!);
    if (options.onSelect && slice.key) {
      path.setAttribute('class', 'oa-chart-hit');
      path.addEventListener('click', () => options.onSelect!(slice.key));
    }

    const title = document.createElementNS(SVG_NS, 'title');
    const share = Math.round((slice.value / total) * 100);
    title.textContent = `${slice.label} — ${options.format(slice.value)} (${share}%)`;
    path.appendChild(title);
    node.appendChild(path);

    angle = end;
  });
  return node;
}

/**
 * The legend carries the numbers.
 *
 * Both shapes need it: a bar chart without labels is a set of anonymous
 * rectangles, and a pie chart's own labels never fit. Keeping it identical
 * for both is also what makes the toggle feel like one chart in two shapes.
 */
function legend(data: Slice[], colours: string[], options: ChartOptions): HTMLElement {
  const total = data.reduce((sum, slice) => sum + slice.value, 0);
  const list = el('ul', 'oa-chart-legend');

  data.forEach((slice, index) => {
    const row = el('li', 'oa-chart-legend-row');

    const swatch = el('span', 'oa-chart-swatch');
    swatch.style.background = colours[index]!;
    row.appendChild(swatch);

    const name = el('span', 'oa-chart-legend-label', slice.label);
    if (options.onSelect && slice.key) {
      name.classList.add('oa-chart-hit');
      name.addEventListener('click', () => options.onSelect!(slice.key));
    }
    row.appendChild(name);

    const value = el('span', 'oa-chart-legend-value', options.format(slice.value));
    row.appendChild(value);

    const share = total > 0 ? Math.round((slice.value / total) * 100) : 0;
    row.appendChild(el('span', 'oa-chart-legend-share', `${share}%`));

    list.appendChild(row);
  });
  return list;
}
