// The settings plugins put on one workbench page, as that page loads, edits
// and saves them. See PluginSettingsCard.vue for how a section is drawn.

import { computed, reactive } from 'vue';
import { plugins } from '@/plugins/registry';
import type { SettingsSection } from '@/plugins/types';
import type { WorkbenchGroup } from './workbench';

export function usePluginSettings(page: SettingsSection['page']) {
  const sections = computed(() =>
    plugins().flatMap((plugin) => plugin.settings ?? []).filter((section) => section.page === page));
  const draft = reactive<Record<string, string>>({});
  const hints = reactive<Record<string, string>>({});

  /**
   * Fills the draft from what the server sent. Secrets start empty.
   *
   * Only the keys the server sent: it leaves out the ones this account may
   * not write — a plugin can keep where its service lives to a super
   * administrator — and a key in the draft is a key the save sends, so one
   * the account could not see would have the whole page's save refused.
   */
  function load(values: Record<string, string>): void {
    for (const section of sections.value) {
      for (const control of section.controls) {
        if (!(control.key in values)) {
          delete draft[control.key];
          delete hints[control.key];
          continue;
        }
        if (control.kind === 'secret') {
          draft[control.key] = '';
          hints[control.key] = values[control.key] ?? '';
        } else {
          draft[control.key] = values[control.key] ?? section.defaults[control.key] ?? '';
        }
      }
    }
  }

  /**
   * The draft as the page's save sends it. An empty secret keeps what is
   * stored — the field was never shown it — which is the bargain every other
   * secret on these pages makes.
   */
  function collect(): Record<string, string> {
    const out: Record<string, string> = {};
    for (const section of sections.value) {
      for (const control of section.controls) {
        if (!(control.key in draft)) continue;
        const value = draft[control.key] ?? '';
        out[control.key] = control.kind === 'text' || control.kind === 'secret' ? value.trim() : value;
      }
    }
    return out;
  }

  /** The page's categories with each plugin section listed under its own. */
  function withSections(groups: WorkbenchGroup[]): WorkbenchGroup[] {
    return groups.map((group) => ({
      ...group,
      sections: [...group.sections, ...sections.value.filter((s) => s.category === group.id).map((s) => s.id)],
    }));
  }

  /** The page's two columns, likewise. */
  function withColumns(columns: [string[], string[]]): [string[], string[]] {
    return [
      [...columns[0], ...sections.value.filter((s) => s.column === 0).map((s) => s.id)],
      [...columns[1], ...sections.value.filter((s) => s.column === 1).map((s) => s.id)],
    ];
  }

  /** What the page's search should match each plugin section by. */
  const words = computed<Record<string, string[]>>(() => Object.fromEntries(sections.value.map((section) => [
    section.id,
    [section.title(), section.hint?.() ?? '', ...section.controls.map((c) => c.label()), ...(section.keywords ?? [])],
  ])));

  return { sections, draft, hints, load, collect, withSections, withColumns, words };
}
