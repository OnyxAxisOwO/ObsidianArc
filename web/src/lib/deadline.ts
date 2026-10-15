// A plugin guard's SDK can take a while and has no deadline of its own, so a
// check that never answers would leave the submit button spinning until the
// page is reloaded. Every view that asks a guard for a token shares this limit,
// because the guard protocol is the same wherever it is asked.
export const GUARD_DEADLINE_MS = 60_000;

export class GuardTimeout extends Error {}

export function withDeadline<T>(work: Promise<T>, ms: number): Promise<T> {
  return new Promise<T>((resolve, reject) => {
    const timer = window.setTimeout(() => reject(new GuardTimeout()), ms);
    work.then(
      (value) => { window.clearTimeout(timer); resolve(value); },
      (failure: unknown) => { window.clearTimeout(timer); reject(failure); },
    );
  });
}

// Boot waits on some loads before the first paint, and a request that is
// accepted and never answers would hold the whole page on its spinner. Whatever
// has not answered by the deadline is treated as a failure, and the caller
// decides what a failure costs.
export function within<T>(work: Promise<T>, ms: number): Promise<T> {
  let timer: ReturnType<typeof setTimeout> | undefined;
  const expired = new Promise<never>((_, reject) => {
    timer = setTimeout(() => reject(new Error(`no answer after ${ms} ms`)), ms);
  });
  return Promise.race([work, expired]).finally(() => clearTimeout(timer));
}
