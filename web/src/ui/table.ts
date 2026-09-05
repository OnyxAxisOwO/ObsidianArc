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
}

export interface TableOptions<T> {
  columns: Array<Column<T>>;
  rows: T[];
  empty: string;
  onSelect?(row: T): void;
  /** Marks a row as inactive — a disabled account, a switched-off model. */
  muted?(row: T): boolean;
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
  for (const column of options.columns) {
    const th = el('th', columnClass(column), column.header);
    if (column.width) th.style.width = column.width;
    headRow.appendChild(th);
  }
  thead.appendChild(headRow);
  table.appendChild(thead);

  const tbody = el('tbody');
  for (const row of options.rows) {
    const tr = el('tr', options.muted?.(row) ? 'muted' : null);
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
  }
  table.appendChild(tbody);

  wrap.appendChild(table);
  return wrap;
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

export function badge(text: string, tone: 'default' | 'muted' | 'danger' = 'default'): HTMLElement {
  const classes = ['oa-badge'];
  if (tone === 'muted') classes.push('oa-badge-muted');
  if (tone === 'danger') classes.push('oa-badge-danger');
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
