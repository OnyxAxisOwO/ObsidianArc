// The table the administration screens list things in.
//
// Rows are clickable and open a drawer; there is no inline editing and no
// per-row button menu, because a table of forty rows with six controls each
// is a wall. The cell renderers return nodes rather than HTML strings, for
// the same reason the transcript does.

import { t } from '../i18n';
import { el } from './dom';

export interface Column<T> {
  header: string;
  /** A cell is text, or a node when it needs a badge or a meter. */
  cell(row: T): string | Node;
  /** Right-aligns and tabular-numbers the column. */
  numeric?: boolean;
  /** Hidden below 720px, for the columns a phone has no room for. */
  secondary?: boolean;
  width?: string;
  /**
   * Makes the header clickable, and says what to order rows by. Separate
   * from `cell` because what a cell shows is rarely what it sorts by: a
   * state column shows two badges, and a weight column shows "1×".
   */
  sort?(row: T): string | number;
}

/** Which column the rows are ordered by, and which way. */
export interface SortState {
  column: number;
  descending: boolean;
}

export interface TableOptions<T> {
  columns: Array<Column<T>>;
  rows: T[];
  empty: string;
  onSelect?(row: T): void;
  /** Marks a row as inactive — a disabled account, a switched-off model. */
  muted?(row: T): boolean;
  /**
   * Held by the caller rather than by the table, so that a re-render for
   * some other reason — a filter changing, a row being edited — does not
   * throw away the order the reader chose. Absent leaves the rows in the
   * order they arrived, which is the order the server sorted them in.
   */
  sort?: SortState | null;
  onSort?(next: SortState): void;
  /**
   * Makes the rows draggable, and hands back the whole list in the order they
   * were left in.
   *
   * Ignored while a column sort is on: the rows would be showing an order
   * nobody stored, and dropping one into it would be writing down a position
   * the reader never actually chose.
   */
  onReorder?(rows: T[]): void;
}

export function renderTable<T>(options: TableOptions<T>): HTMLElement {
  const wrap = el('div', 'oa-table-wrap');

  if (!options.rows.length) {
    wrap.appendChild(el('p', 'oa-table-empty', options.empty));
    return wrap;
  }

  const table = el('table', 'oa-table');
  const thead = el('thead');
  const headRow = el('tr');
  options.columns.forEach((column, index) => {
    const th = el('th', columnClass(column), column.header);
    if (column.width) th.style.width = column.width;
    if (column.sort && options.onSort) {
      const active = options.sort?.column === index;
      th.classList.add('sortable');
      if (active) th.classList.add(options.sort!.descending ? 'desc' : 'asc');
      // aria-sort rather than a marker in the text: the arrow is decoration
      // and a screen reader should hear the state, not read an arrow.
      th.setAttribute('aria-sort', active ? (options.sort!.descending ? 'descending' : 'ascending') : 'none');
      th.tabIndex = 0;
      // The same column again reverses; a different one starts ascending,
      // because arriving at a column already reversed reads as a bug.
      const toggle = () => options.onSort!({ column: index, descending: active && !options.sort!.descending });
      th.addEventListener('click', toggle);
      th.addEventListener('keydown', (event) => {
        if (event.key === 'Enter' || event.key === ' ') {
          event.preventDefault();
          toggle();
        }
      });
    }
    headRow.appendChild(th);
  });
  thead.appendChild(headRow);
  table.appendChild(thead);

  const ordered = sortRows(options);
  const draggable = !!options.onReorder && !options.sort;
  let dragging: number | null = null;

  const tbody = el('tbody');
  ordered.forEach((row, index) => {
    const tr = el('tr', options.muted?.(row) ? 'muted' : null);
    if (draggable) {
      tr.draggable = true;
      tr.classList.add('draggable');

      tr.addEventListener('dragstart', (event) => {
        dragging = index;
        tr.classList.add('dragging');
        // Firefox starts no drag at all without a payload on the transfer.
        event.dataTransfer?.setData('text/plain', String(index));
        if (event.dataTransfer) event.dataTransfer.effectAllowed = 'move';
      });

      tr.addEventListener('dragover', (event) => {
        if (dragging === null || dragging === index) return;
        // Without preventDefault the browser refuses the drop, which reads as
        // the row springing back for no reason.
        event.preventDefault();
        if (event.dataTransfer) event.dataTransfer.dropEffect = 'move';
        clearMarks();
        tr.classList.add(index > dragging ? 'drop-after' : 'drop-before');
      });

      tr.addEventListener('drop', (event) => {
        event.preventDefault();
        const from = dragging;
        clearMarks();
        dragging = null;
        if (from === null || from === index) return;
        const next = [...ordered];
        const [moved] = next.splice(from, 1);
        next.splice(index, 0, moved!);
        options.onReorder!(next);
      });

      tr.addEventListener('dragend', () => {
        dragging = null;
        tr.classList.remove('dragging');
        clearMarks();
      });
    }
    if (options.onSelect) {
      tr.classList.add('selectable');
      tr.tabIndex = 0;
      tr.addEventListener('click', () => options.onSelect!(row));
      tr.addEventListener('keydown', (event) => {
        if (event.key === 'Enter' || event.key === ' ') {
          event.preventDefault();
          options.onSelect!(row);
        }
      });
    }
    for (const column of options.columns) {
      const td = el('td', columnClass(column));
      const content = column.cell(row);
      if (typeof content === 'string') td.textContent = content;
      else td.appendChild(content);
      tr.appendChild(td);
    }
    tbody.appendChild(tr);
  });
  table.appendChild(tbody);

  // Only the two marks, not the row being carried: that one keeps its class
  // until the drag ends, so it stays faded the whole way across.
  function clearMarks(): void {
    for (const node of tbody.children) {
      node.classList.remove('drop-before', 'drop-after');
    }
  }

  wrap.appendChild(table);
  return wrap;
}

/**
 * A copy, never the caller's array: the caller is holding the unsorted list
 * to filter and re-render from, and reordering it under them would make the
 * order depend on how many times the table happened to be drawn.
 */
function sortRows<T>(options: TableOptions<T>): T[] {
  const state = options.sort;
  const by = state ? options.columns[state.column]?.sort : undefined;
  if (!state || !by) return options.rows;

  const direction = state.descending ? -1 : 1;
  return [...options.rows].sort((left, right) => {
    const a = by(left);
    const b = by(right);
    if (typeof a === 'number' && typeof b === 'number') return (a - b) * direction;
    // localeCompare, not <: the names in this interface are as often Chinese
    // as English, and code-point order puts every Han character after Z.
    return String(a).localeCompare(String(b)) * direction;
  });
}

function columnClass<T>(column: Column<T>): string {
  const names: string[] = [];
  if (column.numeric) names.push('numeric');
  if (column.secondary) names.push('secondary');
  return names.join(' ');
}

/** Two lines in one cell: a name and the thing that disambiguates it. */
export function stacked(title: string, sub?: string): HTMLElement {
  const wrap = el('div', 'oa-cell-stack');
  wrap.appendChild(el('span', 'oa-cell-title', title));
  if (sub) wrap.appendChild(el('span', 'oa-cell-sub', sub));
  return wrap;
}

export function badge(text: string, tone: 'default' | 'muted' | 'danger' | 'warning' = 'default'): HTMLElement {
  const classes = ['oa-badge'];
  if (tone === 'muted') classes.push('oa-badge-muted');
  if (tone === 'danger') classes.push('oa-badge-danger');
  if (tone === 'warning') classes.push('oa-badge-warning');
  return el('span', classes.join(' '), text);
}

export function badges(...nodes: Array<Node | null>): HTMLElement {
  const wrap = el('div', 'oa-badge-row');
  for (const node of nodes) if (node) wrap.appendChild(node);
  return wrap;
}

// --- number formatting ---------------------------------------------------------

/** 12345 → "12.3k". Tables are read at a glance, not audited in. */
export function compactNumber(value: number): string {
  if (!Number.isFinite(value)) return '—';
  if (Math.abs(value) < 1000) {
    return Number.isInteger(value) ? String(value) : String(Math.round(value * 100) / 100);
  }
  if (Math.abs(value) < 1_000_000) return `${Math.round(value / 100) / 10}k`;
  return `${Math.round(value / 100_000) / 10}M`;
}

export function relativeTime(at: number): string {
  if (!at) return '—';
  const seconds = Math.round((Date.now() - at) / 1000);
  if (seconds < 60) return t('timeJustNow');
  const minutes = Math.round(seconds / 60);
  if (minutes < 60) return t('timeMinutes', { count: minutes });
  const hours = Math.round(minutes / 60);
  if (hours < 48) return t('timeHours', { count: hours });
  const days = Math.round(hours / 24);
  if (days < 30) return t('timeDays', { count: days });
  return new Date(at).toLocaleDateString();
}

export function absoluteTime(at: number): string {
  if (!at) return '—';
  return new Date(at).toLocaleString();
}

/**
 * Beside the other two rather than in the admin screens, where it used to
 * live: the About panel needs it, and one import of one four-line helper was
 * enough to pull the whole backoffice into the main bundle with it.
 */
export function formatUptime(seconds: number): string {
  if (seconds < 60) return `${seconds}s`;
  if (seconds < 3600) return `${Math.round(seconds / 60)}m`;
  if (seconds < 86400) return `${Math.round(seconds / 3600)}h`;
  return `${Math.round(seconds / 86400)}d`;
}
