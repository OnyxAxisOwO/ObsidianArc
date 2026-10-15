// The modal surfaces open now, oldest first: side panels and sheets.
//
// Escape goes to the last of them only. A drawer, a sheet opened over it and
// the account panel beside it all listen on document, and one key used to close
// all of them: the drawer lost its unsaved input, and a confirmation sheet took
// the panel it was asked from along with it. A surface leaves the stack when it
// unmounts rather than when it starts to close, so a second Escape during a
// slide-out does not reach the surface beneath.

const open: object[] = [];

export function openModal(token: object): void {
  open.push(token);
}

export function closeModal(token: object): void {
  const at = open.indexOf(token);
  if (at !== -1) open.splice(at, 1);
}

export function isTopModal(token: object): boolean {
  return open[open.length - 1] === token;
}
