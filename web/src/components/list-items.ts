// The rows the two list controls take.
//
// Shared shape, two behaviours: a checkbox list answers "which of these", a
// tier list answers "how much of each". Their items are identical, which is
// why they are declared once here rather than twice inside two components
// that could then drift.

export interface ListItem {
  value: string;
  label: string;
  sub?: string | undefined;
}

/** What a group may do with a model: use it, see it, or neither. */
export type AccessTier = 'use' | 'view' | 'none';
