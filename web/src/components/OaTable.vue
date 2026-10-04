<script setup lang="ts" generic="T">
// The table the administration screens list things in.
//
// Rows are clickable and open a panel; there is no inline editing and no
// per-row button menu, because a table of forty rows with six controls each
// is a wall.
//
// A column declares its header and how to sort by it; what a cell actually
// contains is a slot named after the column, so a cell can be a badge, a
// meter or two stacked lines without this component knowing about any of
// them. `text` is the shortcut for the common case where it is a string.

import { computed, ref, watch } from 'vue';
import { t } from '@/composables/useI18n';
import { rememberedPageSize } from '@/lib/page-size';
import type { Column, SortState, PageState } from './table-types';
import OaPagination from './OaPagination.vue';

const props = defineProps<{
  columns: ReadonlyArray<Column<T>>;
  rows: readonly T[];
  empty: string;
  /** Marks a row as inactive — a disabled account, a switched-off model. */
  muted?: ((row: T) => boolean) | undefined;
  /**
   * Held by the caller rather than by the table, so that a re-render for some
   * other reason — a filter changing, a row being edited — does not throw
   * away the order the reader chose. Null leaves the rows in the order they
   * arrived, which is the order the server sorted them in.
   */
  sort?: SortState | null;
  selectable?: boolean;
  /**
   * Makes the rows draggable, and hands back the whole list in the order they
   * were left in. Ignored while a column sort is on: the rows would be
   * showing an order nobody stored, and dropping one into it would be writing
   * down a position the reader never actually chose.
   */
  reorderable?: boolean;
  pagination?: PageState & { total: number };
  busy?: boolean;
  /**
   * A checkbox column, for acting on several rows at once. Clicking the row
   * still opens it; only the box selects. Needs `rowKey`, because a selection
   * has to outlive the redraw that follows the action it was made for.
   */
  multi?: boolean;
  selected?: readonly string[];
  rowKey?: (row: T) => string;
}>();

const emit = defineEmits<{
  (event: 'update:selected', keys: string[]): void;
  (event: 'select', row: T): void;
  (event: 'sort', next: SortState | null): void;
  (event: 'reorder', rows: T[]): void;
  (event: 'page', next: PageState): void;
}>();

const local = ref<PageState>({ page: 1, pageSize: rememberedPageSize() });
const paging = computed(() => props.pagination ?? { ...local.value, total: props.rows.length });
const start = computed(() => (paging.value.page - 1) * paging.value.pageSize);
const visible = computed(() => props.pagination ? ordered.value : ordered.value.slice(start.value, start.value + paging.value.pageSize));
const wrap = ref<HTMLElement | null>(null);
function changePage(next: PageState): void {
  if (props.pagination) emit('page', next);
  else local.value = next;
  wrap.value?.scrollIntoView?.({ block: 'nearest' });
}
watch(() => props.rows, () => {
  // Filtering may remove the page being read; keep the nearest valid page.
  local.value.page = Math.min(local.value.page, Math.max(1, Math.ceil(props.rows.length / local.value.pageSize)));
});
watch(() => props.sort, () => { local.value.page = 1; });

const chosen = computed(() => new Set(props.selected ?? []));
const keyOf = (row: T): string => props.rowKey!(row);

function toggleRow(row: T, on: boolean): void {
  const key = keyOf(row);
  const next = (props.selected ?? []).filter((entry) => entry !== key);
  if (on) next.push(key);
  emit('update:selected', next);
}

// The header box speaks for the page in front of the reader. Rows on other
// pages are selected by the bar's own "select all", where the count it prints
// says how many that is.
const pageKeys = computed(() => visible.value.map(keyOf));
const pageAll = computed(() => pageKeys.value.length > 0 && pageKeys.value.every((key) => chosen.value.has(key)));
const pageSome = computed(() => pageKeys.value.some((key) => chosen.value.has(key)));

function togglePage(on: boolean): void {
  const page = new Set(pageKeys.value);
  const next = (props.selected ?? []).filter((entry) => !page.has(entry));
  if (on) next.push(...pageKeys.value);
  emit('update:selected', next);
}

// A row that was filtered out or deleted stays selected otherwise, and the
// next bulk action would reach something the reader can no longer see.
watch(() => props.rows, (rows) => {
  if (!props.multi || !props.selected?.length) return;
  const live = new Set(rows.map(keyOf));
  const kept = props.selected.filter((entry) => live.has(entry));
  if (kept.length !== props.selected.length) emit('update:selected', kept);
});

/**
 * A copy, never the caller's array: the caller is holding the unsorted list
 * to filter and re-render from, and reordering it under them would make the
 * order depend on how many times the table happened to be drawn.
 */
const ordered = computed<T[]>(() => {
  const state = props.sort;
  const by = state ? props.columns[state.column]?.sort : undefined;
  if (!state || !by) return [...props.rows];

  const direction = state.descending ? -1 : 1;
  return [...props.rows].sort((left, right) => {
    const a = by(left);
    const b = by(right);
    if (typeof a === 'number' && typeof b === 'number') return (a - b) * direction;
    // localeCompare, not <: the names in this interface are as often Chinese
    // as English, and code-point order puts every Han character after Z.
    return String(a).localeCompare(String(b)) * direction;
  });
});

const draggable = computed(() => !!props.reorderable && !props.sort);

const dragging = ref<number | null>(null);
const dropBefore = ref<number | null>(null);
const dropAfter = ref<number | null>(null);

function columnClass(column: Column<T>): string {
  const names: string[] = [];
  if (column.numeric) names.push('numeric');
  if (column.secondary) names.push('secondary');
  return names.join(' ');
}

function sortState(index: number): 'ascending' | 'descending' | 'none' {
  if (props.sort?.column !== index) return 'none';
  return props.sort.descending ? 'descending' : 'ascending';
}

// A different column starts ascending; the same column again reverses to
// descending; a third click restores natural unsorted order (null) so that
// drag-and-drop reordering becomes available again.
function toggleSort(index: number): void {
  if (props.sort?.column !== index) {
    emit('sort', { column: index, descending: false });
  } else if (!props.sort.descending) {
    emit('sort', { column: index, descending: true });
  } else {
    emit('sort', null);
  }
}

function onDragStart(event: DragEvent, index: number): void {
  dragging.value = index + start.value;
  // Firefox starts no drag at all without a payload on the transfer.
  event.dataTransfer?.setData('text/plain', String(index));
  if (event.dataTransfer) event.dataTransfer.effectAllowed = 'move';
}

function onDragOver(event: DragEvent, index: number): void {
  index += start.value;
  if (dragging.value === null || dragging.value === index) return;
  // Without preventDefault the browser refuses the drop, which reads as the
  // row springing back for no reason.
  event.preventDefault();
  if (event.dataTransfer) event.dataTransfer.dropEffect = 'move';
  dropBefore.value = index < dragging.value ? index : null;
  dropAfter.value = index > dragging.value ? index : null;
}

function onDrop(event: DragEvent, index: number): void {
  index += start.value;
  event.preventDefault();
  const from = dragging.value;
  clearMarks();
  dragging.value = null;
  if (from === null || from === index) return;
  const next = [...ordered.value];
  const [moved] = next.splice(from, 1);
  next.splice(index, 0, moved!);
  emit('reorder', next);
}

function clearMarks(): void {
  dropBefore.value = null;
  dropAfter.value = null;
}
</script>

<template>
  <div class="oa-table-block" :aria-busy="busy">
  <div ref="wrap" class="oa-table-wrap" tabindex="0">
    <p v-if="!props.rows.length" class="oa-table-empty">{{ props.empty }}</p>
    <!-- A block establishes the actual table width before fixed column layout.
         A min-width on the table itself can still leave columns compressed.
         A column that names no width is counted at 140 here, for the point at
         which the table starts to scroll, but is given no width on its
         header: in a fixed layout that is the column that takes whatever the
         sized ones leave — the name, usually — instead of every column being
         stretched in proportion and the figures drifting apart. -->

    <div v-else :style="{ minWidth: `${(props.multi ? 40 : 0) + props.columns.reduce((sum, col) => sum + (Number.parseInt(col.width ?? '') || 140), 0)}px` }">
    <table class="oa-table">
      <thead>
        <tr>
          <th v-if="props.multi" class="oa-table-check" @click="togglePage(!pageAll)">
            <input
              type="checkbox"
              :checked="pageAll"
              :indeterminate="pageSome && !pageAll"
              :aria-label="t('selectPage')"
              @click.stop
              @change="togglePage(($event.target as HTMLInputElement).checked)"
            >
          </th>
          <th
            v-for="(column, index) in props.columns"
            :key="column.key"
            :class="[
              columnClass(column),
              {
                sortable: !!column.sort,
                asc: props.sort?.column === index && !props.sort.descending,
                desc: props.sort?.column === index && props.sort.descending,
              },
            ]"
            :style="column.width ? { width: column.width } : undefined"
            :aria-sort="column.sort ? sortState(index) : undefined"
            :tabindex="column.sort ? 0 : undefined"
            @click="column.sort && toggleSort(index)"
            @keydown.enter.prevent="column.sort && toggleSort(index)"
            @keydown.space.prevent="column.sort && toggleSort(index)"
          >
            {{ column.header }}
          </th>
        </tr>
      </thead>
      <tbody>
        <tr
          v-for="(row, index) in visible"
          :key="index"
          :class="{
            muted: props.muted?.(row),
            checked: props.multi && chosen.has(keyOf(row)),
            selectable: props.selectable,
            draggable: draggable,
            dragging: dragging === index + start,
            'drop-before': dropBefore === index + start,
            'drop-after': dropAfter === index + start,
          }"
          :draggable="draggable"
          :tabindex="props.selectable ? 0 : undefined"
          @click="props.selectable && emit('select', row)"
          @keydown.enter.prevent="props.selectable && emit('select', row)"
          @keydown.space.prevent="props.selectable && emit('select', row)"
          @dragstart="draggable && onDragStart($event, index)"
          @dragover="draggable && onDragOver($event, index)"
          @drop="draggable && onDrop($event, index)"
          @dragend="dragging = null; clearMarks()"
        >
          <td
            v-if="props.multi"
            class="oa-table-check"
            @click.stop="toggleRow(row, !chosen.has(keyOf(row)))"
            @keydown.stop
          >
            <input
              type="checkbox"
              :checked="chosen.has(keyOf(row))"
              :aria-label="t('selectRow')"
              @click.stop
              @change="toggleRow(row, ($event.target as HTMLInputElement).checked)"
            >
          </td>
          <td v-for="column in props.columns" :key="column.key" :class="columnClass(column)">
            <slot :name="`cell-${column.key}`" :row="row">{{ column.text?.(row) ?? '' }}</slot>
          </td>
        </tr>
      </tbody>
    </table>
    </div>
  </div>
  <OaPagination v-bind="paging" :busy="busy" @change="changePage" />
  </div>
</template>
