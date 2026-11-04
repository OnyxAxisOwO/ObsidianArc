import { onScopeDispose } from 'vue';

/**
 * A delayed call owned by the component that made it. Calling the returned
 * function again cancels the call still waiting, so an earlier press cannot
 * clear a confirmation that a later press has just put up. Leaving the screen
 * cancels whatever is waiting, so nothing runs against a component that has
 * already been torn down.
 *
 * Must be called during setup, where the component's scope is active.
 */
export function useLatestTimeout(): (run: () => void, ms: number) => void {
  let pending = 0;
  onScopeDispose(() => window.clearTimeout(pending));
  return (run, ms) => {
    window.clearTimeout(pending);
    pending = window.setTimeout(run, ms);
  };
}
