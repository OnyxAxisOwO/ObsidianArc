/** Dispatched on window when a request is refused because the session it was
 *  sent with has ended. Every page that was open sees the same refusal, so the
 *  session store listens for this rather than each page handling it.
 *
 *  Kept out of the client module so the store can name the event without
 *  importing the client it is itself used by. */
export const SESSION_ENDED = 'oa-session-ended';

/** Dispatched on window only by a tab that was signed in and has just been
 *  signed out by SESSION_ENDED. A tab that was already anonymous never sends
 *  it, so the router can send a reader off a protected page without touching
 *  a visitor who was never on one. */
export const SESSION_LOST = 'oa-session-lost';
