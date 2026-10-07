// What the backoffice asks of a plugin's controls before an action runs:
// which are on screen, given the rest of the draft, and whether one that is
// on screen and required is still empty. A hidden control is never required —
// it is hidden because the other answers made it irrelevant.

import type { ActionControl, ActionDraft } from '@/plugins/types';

export function visibleControls(controls: readonly ActionControl[], draft: ActionDraft): ActionControl[] {
  return controls.filter((control) => !control.visible || control.visible(draft));
}

export function missingRequired(controls: readonly ActionControl[], draft: ActionDraft): boolean {
  return controls.some((control) => {
    const value = (draft[control.key] ?? '').trim();
    switch (control.kind) {
      case 'text':
      case 'textarea':
      case 'number':
      case 'datetime':
        return Boolean(control.required) && !value;
      case 'rows':
        return control.min !== undefined && countRows(value) < control.min;
      default:
        return false;
    }
  });
}

function countRows(value: string): number {
  try {
    const parsed: unknown = JSON.parse(value || '[]');
    return Array.isArray(parsed) ? parsed.length : 0;
  } catch {
    return 0;
  }
}
