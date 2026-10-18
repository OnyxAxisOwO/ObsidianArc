// How a request ended, in words. The usage page names the same four outcomes
// in a badge, a breakdown and a filter, and the three used to spell the table
// out separately; any status the server adds later reads as a failure in all
// of them at once rather than in whichever copy was updated.

import type { StringKey } from '@/composables/useI18n';

export function outcomeLabel(status: string): StringKey {
  switch (status) {
    case 'ok': return 'statusOk';
    case 'aborted': return 'statusStopped';
    case 'rejected': return 'statusRefused';
    default: return 'statusFailed';
  }
}
