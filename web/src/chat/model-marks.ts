// The marks a model can wear in the picker.
//
// Two kinds. The vendors' own marks (vendor-marks.ts, from lobe-icons) are
// what a reader recognises a family by; they are fourteen kilobytes of path
// data, so they arrive as a chunk of their own the first time a tile is drawn
// rather than on everyone's first paint, and a tile shows its initials until
// then. A few generic marks are drawn here, for a model that is nobody's in
// particular. Both are painted in currentColor, so no mark brings a hue of
// its own and all of them follow the theme.
//
// A model stores which one it wears in its avatar field, as `mark:<id>`. An
// empty avatar means "guess from the name", which is what most instances will
// leave it as; `mark:none` asks for the initials instead.

import { computed, shallowRef } from 'vue';
import type { StringKey } from '@/i18n';

export interface MarkShape {
  d: string;
  /** Filled rather than stroked. */
  fill?: boolean;
  transform?: string;
}

export interface ModelMark {
  id: string;
  /** A family's own name, shown as written; or a key for the generic ones. */
  name?: string;
  labelKey?: StringKey;
  shapes: MarkShape[];
}

/** Square cells on a grid, from rows written as strings of # and space. */
function cells(rows: string[], size: number, gap: number): MarkShape[] {
  const width = rows[0]!.length * (size + gap) - gap;
  const height = rows.length * (size + gap) - gap;
  const left = (24 - width) / 2;
  const top = (24 - height) / 2;
  const out: MarkShape[] = [];
  rows.forEach((row, y) => [...row].forEach((cell, x) => {
    if (cell !== '#') return;
    const px = left + x * (size + gap);
    const py = top + y * (size + gap);
    out.push({ d: `M${px} ${py}h${size}v${size}h${-size}z`, fill: true });
  }));
  return out;
}

/** The generic marks, which are always here. */
const GENERIC_MARKS: readonly ModelMark[] = [
  {
    id: 'spark',
    labelKey: 'markSpark',
    shapes: [{ d: 'M12 3.5l1.9 6.6 6.6 1.9-6.6 1.9-1.9 6.6-1.9-6.6L3.5 12l6.6-1.9z', fill: true }],
  },
  { id: 'bolt', labelKey: 'markBolt', shapes: [{ d: 'M13.2 2.8L5 13.6h6.2l-1 7.6 8.2-10.8h-6.2z', fill: true }] },
  {
    id: 'cube',
    labelKey: 'markCube',
    shapes: [{ d: 'M12 3l8 4.5v9L12 21l-8-4.5v-9z' }, { d: 'M4 7.5l8 4.5 8-4.5' }, { d: 'M12 12v9' }],
  },
  { id: 'grid', labelKey: 'markGrid', shapes: cells(['###', '# #', '###'], 4.2, 1.4) },
];

const vendor = shallowRef<readonly ModelMark[]>([]);
let loading: Promise<void> | null = null;

/**
 * Fetches the vendors' marks once. Safe to call from every tile: the first
 * call starts the import and the rest share it. A failed fetch leaves the
 * initials, and the next tile drawn tries again.
 */
export function loadVendorMarks(): Promise<void> {
  loading ??= import('./vendor-marks')
    .then(({ VENDOR_MARKS }) => {
      vendor.value = VENDOR_MARKS.map((mark) => ({
        id: mark.id,
        name: mark.name,
        shapes: mark.paths.map((d) => ({ d, fill: true })),
      }));
    })
    .catch(() => { loading = null; });
  return loading;
}

/** Every mark an operator can choose, the vendors' first once they are here. */
export const modelMarks = computed<readonly ModelMark[]>(() => [...vendor.value, ...GENERIC_MARKS]);
const byID = computed(() => new Map(modelMarks.value.map((mark) => [mark.id, mark])));

/** The family a name most likely belongs to, by the words vendors put in them. */
const GUESSES: ReadonlyArray<[RegExp, string]> = [
  [/claude|anthropic/, 'claude'],
  [/deepseek/, 'deepseek'],
  [/gemini|gemma|google/, 'gemini'],
  [/qwen|qwq|tongyi|通义/, 'qwen'],
  [/mistral|mixtral|codestral|magistral|devstral/, 'mistral'],
  [/llama|meta/, 'meta'],
  [/grok|xai/, 'grok'],
  [/kimi|moonshot/, 'kimi'],
  [/glm|zhipu|智谱/, 'zhipu'],
  [/doubao|豆包|skylark/, 'doubao'],
  [/minimax|abab/, 'minimax'],
  // Last, because "o3" and "gpt" are short enough to turn up inside others.
  [/gpt|openai|chatgpt|(?:^|[^a-z])o[1-9](?:$|[^a-z0-9])/, 'openai'],
];

export function guessMark(name: string): ModelMark | null {
  const text = name.toLowerCase();
  const hit = GUESSES.find(([pattern]) => pattern.test(text));
  return hit ? byID.value.get(hit[1]) ?? null : null;
}

export function markByID(id: string): ModelMark | null {
  return byID.value.get(id) ?? null;
}

export const MARK_PREFIX = 'mark:';
export const MARK_NONE = 'mark:none';
