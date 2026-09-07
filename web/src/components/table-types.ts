export interface Column<T> {
  /** Names the cell slot, and keys the column. */
  key: string;
  header: string;
  /** The shortcut for a cell that is only a string. */
  text?(row: T): string;
  /** Right-aligns and tabular-numbers the column. */
  numeric?: boolean;
  /** Hidden below 720px, for the columns a phone has no room for. */
  secondary?: boolean;
  width?: string;
  /**
   * Makes the header clickable, and says what to order rows by. Separate from
   * the cell because what a cell shows is rarely what it sorts by: a state
   * column shows two badges, and a weight column shows "1×".
   */
  sort?(row: T): string | number;
}

/** Which column the rows are ordered by, and which way. */
export interface SortState {
  column: number;
  descending: boolean;
}
