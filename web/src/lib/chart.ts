// The arithmetic behind the two chart shapes.
//
// No charting library, because one is several hundred kilobytes and its own
// theming system for two shapes this project needs, and because everything
// here has to answer to the accent: a chart drawn in someone else's palette
// is the one part of the interface that ignores the colour the user picked.
//
// Pure, and separate from the component, so the slice geometry can be read
// and tested without a renderer around it.

export interface Slice {
  key: string;
  label: string;
  value: number;
  /** Shown under the label, e.g. the request count behind a credit figure. */
  note?: string;
}

export type ChartShape = 'bar' | 'pie';

export const BAR_WIDTH = 320;
export const BAR_ROW_HEIGHT = 26;
export const BAR_GAP = 6;
export const PIE_SIZE = 200;
export const PIE_RADIUS = 92;

/**
 * The palette a chart draws with.
 *
 * Derived from the accent rather than fixed, by walking lightness across a
 * single hue. That keeps a chart legible on both schemes, keeps it in the
 * user's colour, and degenerates to a grey ramp when the accent is neutral —
 * which is this instance's default and looks deliberate rather than broken.
 */
export function palette(count: number): string[] {
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

/** Folds the tail into one slice, so a pie of forty models stays readable. */
export function fold(data: readonly Slice[], max = 8, otherLabel = 'Other'): Slice[] {
  const sorted = [...data].filter((slice) => slice.value > 0).sort((a, b) => b.value - a.value);
  if (sorted.length <= max) return sorted;

  const head = sorted.slice(0, max - 1);
  const tail = sorted.slice(max - 1);
  head.push({
    key: '',
    label: otherLabel,
    value: tail.reduce((sum, slice) => sum + slice.value, 0),
    note: String(tail.length),
  });
  return head;
}

export interface Wedge {
  slice: Slice;
  colour: string;
  path: string;
  share: number;
}

/**
 * A single slice is a whole circle, which an arc path cannot express: the
 * start and end points coincide and the browser draws nothing. The component
 * draws a <circle> for that case, which is why `path` is empty there.
 */
export function wedges(data: readonly Slice[], colours: readonly string[]): Wedge[] {
  const total = data.reduce((sum, slice) => sum + slice.value, 0);
  if (total <= 0) return [];

  const centre = PIE_SIZE / 2;
  let angle = -Math.PI / 2; // Start at twelve o'clock.

  return data.map((slice, index) => {
    const sweep = (slice.value / total) * Math.PI * 2;
    const end = angle + sweep;
    const path = [
      `M ${centre} ${centre}`,
      `L ${(centre + PIE_RADIUS * Math.cos(angle)).toFixed(2)} ${(centre + PIE_RADIUS * Math.sin(angle)).toFixed(2)}`,
      `A ${PIE_RADIUS} ${PIE_RADIUS} 0 ${sweep > Math.PI ? 1 : 0} 1`,
      `${(centre + PIE_RADIUS * Math.cos(end)).toFixed(2)} ${(centre + PIE_RADIUS * Math.sin(end)).toFixed(2)}`,
      'Z',
    ].join(' ');
    angle = end;
    return {
      slice,
      colour: colours[index] ?? '',
      path,
      share: Math.round((slice.value / total) * 100),
    };
  });
}

export interface Bar {
  slice: Slice;
  colour: string;
  y: number;
  length: number;
}

export function bars(data: readonly Slice[], colours: readonly string[]): Bar[] {
  const largest = Math.max(...data.map((slice) => slice.value));
  return data.map((slice, index) => ({
    slice,
    colour: colours[index] ?? '',
    y: index * (BAR_ROW_HEIGHT + BAR_GAP),
    length: largest > 0 ? Math.max(2, (slice.value / largest) * BAR_WIDTH) : 0,
  }));
}
