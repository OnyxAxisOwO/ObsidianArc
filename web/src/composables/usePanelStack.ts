// Which of the panels in one row is drawn open.
//
// Two panels beside the page make four columns with the rail, and the page
// between them is squeezed to a sliver. Past that point only one panel is
// open at a time and the rest shrink to a strip, the way a window is
// minimised: the newest panel is the open one, and pressing a strip swaps it
// in and sends the others down.
//
// Module state keyed by row, not a store: it exists for the panels of one
// row, is read by exactly the panels in it, and has to be per row because
// the chat and the backoffice never share one.

import { computed, shallowReactive, type ComputedRef } from 'vue';

interface Stack {
  tokens: object[];
  active: object | null;
}

const stacks = new WeakMap<HTMLElement, Stack>();

function stackOf(host: HTMLElement): Stack {
  let stack = stacks.get(host);
  if (!stack) {
    stack = shallowReactive<Stack>({ tokens: [], active: null });
    stacks.set(host, stack);
  }
  return stack;
}

export interface PanelStackEntry {
  /** Whether this panel is one of several and not the one drawn open. */
  collapsed: ComputedRef<boolean>;
  /** Makes this panel the open one. */
  activate(): void;
  /** Gives the place up; the newest of the rest opens in its stead. Idempotent. */
  leave(): void;
}

export function joinPanelStack(host: HTMLElement): PanelStackEntry {
  const stack = stackOf(host);
  const token = {};
  stack.tokens = [...stack.tokens, token];
  stack.active = token;
  return {
    collapsed: computed(() => stack.tokens.length > 1 && stack.active !== token),
    activate() {
      if (stack.tokens.includes(token)) stack.active = token;
    },
    leave() {
      if (!stack.tokens.includes(token)) return;
      stack.tokens = stack.tokens.filter((other) => other !== token);
      if (stack.active === token) stack.active = stack.tokens[stack.tokens.length - 1] ?? null;
    },
  };
}
