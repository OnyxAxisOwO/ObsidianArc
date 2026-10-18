// Every test file starts with the backoffice's English in the dictionary, as a
// page does once the backoffice's chunk has arrived. Most backoffice tests
// mount one screen directly rather than through AdminPage.vue, which is what
// brings it in for a reader; without this they would read key names.
//
// It cannot hide a chat screen that reads a backoffice-only key: which keys
// are the backoffice's is checked on the build, in bundle.test.ts.
import '../src/i18n.admin';
