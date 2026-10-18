// Fixed lengths of time, in milliseconds. A day is 24 hours here as it is on
// the server (24 * time.Hour), not a calendar day, so an expiry or a window
// counted on this screen ends where the server's own count does, even across
// a daylight-saving change.
export const HOUR_MS = 3_600_000;
export const DAY_MS = 86_400_000;
