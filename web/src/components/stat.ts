/** One figure in a stat grid: what it is, what it is, and what qualifies it. */
export interface Stat {
  label: string;
  value: string;
  note?: string | undefined;
}
