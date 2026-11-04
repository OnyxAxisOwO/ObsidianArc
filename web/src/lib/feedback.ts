// How a report's kind, priority and status are named and coloured, for the
// reader's own panel and the backoffice alike. Keys rather than text: the
// views resolve them with t() as they render, so a change of language redraws
// them; a table of words made here would be in whichever language was current
// when this module loaded.

import type { FeedbackKind, FeedbackPriority, FeedbackStatus } from '@/api/feedback';
import type { StringKey } from '@/composables/useI18n';

type Tone = 'default' | 'muted' | 'warning';

export function kindKey(kind: FeedbackKind): StringKey {
  return kind === 'bug' ? 'feedbackKindBug' : 'feedbackKindIdea';
}

/** A priority this build does not know reads as medium, the server's default, rather than failing to draw. */
export function priorityKey(priority: FeedbackPriority): StringKey {
  switch (priority) {
    case 'high': return 'feedbackPriorityHigh';
    case 'low': return 'feedbackPriorityLow';
    default: return 'feedbackPriorityMedium';
  }
}

/** High is the only priority worth colouring: the rest are the normal case. */
export function priorityTone(priority: FeedbackPriority): Tone {
  return priority === 'high' ? 'warning' : 'muted';
}

export function statusKey(status: FeedbackStatus): StringKey {
  return status === 'resolved' ? 'feedbackStatusResolved' : 'feedbackStatusOpen';
}

/** A resolved report steps back; an open one is drawn plain. */
export function statusTone(status: FeedbackStatus): Tone {
  return status === 'resolved' ? 'muted' : 'default';
}
