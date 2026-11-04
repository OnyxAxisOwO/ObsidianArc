/**
 * Whether a rejection is a request cancelled on purpose, which is not a failure
 * to show anyone. Recognised by name rather than by class: a DOMException and
 * an Error both carry the name, and so does whatever a mocked or polyfilled
 * fetch rejects with.
 */
export function isAbortError(err: unknown): boolean {
  return typeof err === 'object' && err !== null && (err as { name?: string }).name === 'AbortError';
}
