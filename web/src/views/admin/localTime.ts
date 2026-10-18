// A datetime-local field reads and writes the wall clock with no zone, so an
// instant is moved by the local offset before it is cut to minutes. Doing the
// shift in one place keeps every expiry field on the same clock.

/** The instant as a datetime-local value, to the minute, on the local clock. */
export function toLocalInput(at: Date): string {
  return new Date(at.getTime() - at.getTimezoneOffset() * 60_000).toISOString().slice(0, 16);
}

/** That many days from now, as a datetime-local value. */
export function daysFromNow(days: number): string {
  const date = new Date();
  date.setDate(date.getDate() + days);
  return toLocalInput(date);
}
