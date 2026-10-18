// Every test file starts with the backoffice's English in the dictionary, as a
// page does once the backoffice's chunk has arrived. Most backoffice tests
// mount one screen directly rather than through AdminPage.vue, which is what
// brings it in for a reader; without this they would read key names.
//
// It cannot hide a chat screen that reads a backoffice-only key: which keys
// are the backoffice's is checked on the build, in bundle.test.ts.
import '../src/i18n.admin';

import { afterEach } from 'vitest';

// Screens that hand the browser to the server are tested with window.location
// replaced, and a replacement that outlives its test turns every later test in
// the file into one against a fake page — whether the test restored it in a
// finally, forgot to, or replaced it in a beforeEach that has no undo. Putting
// the real one back here covers all three. This runs after the file's own
// afterEach hooks, so a teardown that still navigates sees the stub it expects.
const realLocation = Object.getOwnPropertyDescriptor(window, 'location');

afterEach(() => {
  if (realLocation) Object.defineProperty(window, 'location', realLocation);
  else Reflect.deleteProperty(window, 'location');
});
