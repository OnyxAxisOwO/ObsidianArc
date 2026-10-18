// Waits that several test files share. Each file used to carry its own copy,
// and the copies drifted — three rounds in one file, four in the next, for no
// reason either recorded — so a test that passed in one file could fail when
// moved to another. This is not a .test.ts file, so vitest never runs it.
//
// Not every file's settle belongs here: one under fake timers has to advance
// those timers instead, and one waiting for a transition class needs a frame.
// Those keep their own, with the reason beside them.

import { vi } from 'vitest';
import { nextTick } from 'vue';

/** Wall-clock time, for a component's own timer that a test lets run out. */
export const wait = (ms: number): Promise<void> => new Promise((resolve) => setTimeout(resolve, ms));

/**
 * Lets a mounted screen finish what a mock answers at once: each round lets
 * the resolved promises run and Vue draw what they changed. Four rounds is the
 * most any screen here chains — a load that starts another load once it has
 * the first answer — and the rounds cost nothing when there is less to do.
 */
export async function settle(): Promise<void> {
  for (let i = 0; i < 4; i += 1) {
    await new Promise((resolve) => setTimeout(resolve, 0));
    await nextTick();
  }
}

/**
 * Polls until a condition holds. It throws when it gives up, naming what it
 * was waiting for, because a poll that returns quietly hands the failure to
 * the next line — usually a null dereference that says nothing about why.
 */
export async function until(condition: () => boolean, waitingFor: string, timeoutMs = 2000): Promise<void> {
  const deadline = Date.now() + timeoutMs;
  while (!condition()) {
    if (Date.now() > deadline) throw new Error(`timed out waiting for ${waitingFor}`);
    await new Promise((resolve) => setTimeout(resolve, 10));
  }
}

/**
 * Replaces window.location so a hand-off to the server can be seen without
 * navigating jsdom, which refuses one. setup.ts puts the real one back after
 * every test, so a test that forgets to, or fails before it can, does not
 * leave the stub to the tests after it; `restore` is there for a test that
 * needs the real one back before it ends.
 */
export function stubLocation(): { assign: ReturnType<typeof vi.fn>; restore: () => void } {
  const original = window.location;
  const assign = vi.fn();
  const stub = { configurable: true, writable: true };
  Object.defineProperty(window, 'location', { ...stub, value: { assign, href: '' } });
  return {
    assign,
    restore: () => Object.defineProperty(window, 'location', { ...stub, value: original }),
  };
}
