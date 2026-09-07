// A rail that slides away, remembered.
//
// Two screens have one: the conversation list beside the chat, and the
// section list beside the backoffice. They are the same control doing the
// same thing to the same kind of column, so they are one implementation —
// which is also what stops them drifting into behaving differently, the way
// they did when only one of them had a button at all.
//
// One state per storage key, shared by every caller that names it: the header
// button and the rail itself are in different components and must not each
// hold their own idea of whether it is open.

import { ref, type Ref } from 'vue';

export interface CollapsibleRail {
  collapsed: Ref<boolean>;
  toggle(): void;
}

const rails = new Map<string, CollapsibleRail>();

export function useRailCollapse(storageKey: string): CollapsibleRail {
  const existing = rails.get(storageKey);
  if (existing) return existing;

  const collapsed = ref(read(storageKey));
  const rail: CollapsibleRail = {
    collapsed,
    toggle() {
      collapsed.value = !collapsed.value;
      write(storageKey, collapsed.value);
    },
  };
  rails.set(storageKey, rail);
  return rail;
}

function read(key: string): boolean {
  try {
    return localStorage.getItem(key) === '1';
  } catch {
    // Storage disabled: the rail starts open, which is the state that shows
    // the reader everything rather than hiding it.
    return false;
  }
}

function write(key: string, collapsed: boolean): void {
  try {
    localStorage.setItem(key, collapsed ? '1' : '');
  } catch {
    // Best effort; the rail still moved.
  }
}
