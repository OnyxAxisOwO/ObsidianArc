/**
 * Newest-request-wins for answers that can land out of order. A page change or
 * a second click does not wait for the request before it, so the older answer
 * can arrive last; each request takes a ticket, and only the holder of the
 * newest one may write what it fetched.
 *
 * Plain state, no Vue: it owns nothing that needs tearing down, so it can be
 * made anywhere, and several in one component keep their requests apart.
 */
interface LatestRequest {
  /** Takes the next ticket, retiring every one handed out before it. */
  begin(): number;
  /** Whether no request has begun, and nothing was invalidated, since this ticket. */
  isCurrent(ticket: number): boolean;
  /**
   * Retires the request in flight without starting another: the screen moved
   * on (a panel closed, a filter is being typed), and its answer must not land.
   */
  invalidate(): void;
  /**
   * The newest ticket, without taking one. For a follow-up that must yield to
   * any request started after it but must not retire the one in flight itself.
   */
  current(): number;
}

export function useLatestRequest(): LatestRequest {
  let latest = 0;
  return {
    begin: () => ++latest,
    isCurrent: (ticket) => ticket === latest,
    invalidate: () => {
      latest += 1;
    },
    current: () => latest,
  };
}
