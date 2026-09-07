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

import { computed, ref } from 'vue';
import type { Column, SortState } from './table-types';

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
}>();

const emit = defineEmits<{
  (event: 'select', row: T): void;
  (event: 'sort', next: SortState): void;
  (event: 'reorder', rows: T[]): void;
}>();

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

// The same column again reverses; a different one starts ascending, because
// arriving at a column already reversed reads as a bug.
function toggleSort(index: number): void {
  const active = props.sort?.column === index;
  emit('sort', { column: index, descending: active && !props.sort!.descending });
}

function onDragStart(event: DragEvent, index: number): void {
  dragging.value = index;
  // Firefox starts no drag at all without a payload on the transfer.
  event.dataTransfer?.setData('text/plain', String(index));
  if (event.dataTransfer) event.dataTransfer.effectAllowed = 'move';
}

function onDragOver(event: DragEvent, index: number): void {
  if (dragging.value === null || dragging.value === index) return;
  // Without preventDefault the browser refuses the drop, which reads as the
  // row springing back for no reason.
  event.preventDefault();
  if (event.dataTransfer) event.dataTransfer.dropEffect = 'move';
  dropBefore.value = index < dragging.value ? index : null;
  dropAfter.value = index > dragging.value ? index : null;
}

function onDrop(event: DragEvent, index: number): void {
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
  <div class="oa-table-wrap">
    <p v-if="!props.rows.length" class="oa-table-empty">{{ props.empty }}</p>
    <table v-else class="oa-table">
      <thead>
        <tr>
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
          v-for="(row, index) in ordered"
          :key="index"
          :class="{
            muted: props.muted?.(row),
            selectable: props.selectable,
            draggable: draggable,
            dragging: dragging === index,
            'drop-before': dropBefore === index,
            'drop-after': dropAfter === index,
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
          <td v-for="column in props.columns" :key="column.key" :class="columnClass(column)">
            <slot :name="`cell-${column.key}`" :row="row">{{ column.text?.(row) ?? '' }}</slot>
          </td>
        </tr>
      </tbody>
    </table>
  </div>
</template>
