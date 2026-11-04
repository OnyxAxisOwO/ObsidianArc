// The settings plugins put on one workbench page, as that page loads, edits
// and saves them. See PluginSettingsCard.vue for how a section is drawn.

import { computed, reactive } from 'vue';
import { plugins } from '@/plugins/registry';
import type { SettingsSection } from '@/plugins/types';
import type { WorkbenchGroup } from './workbench';

type Control = SettingsSection['controls'][number];

// Text and secret values are trimmed on the way out, so a stray space typed
// after a key or a token is not what gets stored. Other kinds are sent as typed.
function outgoing(kind: Control['kind'], value: string): string {
  return kind === 'text' || kind === 'secret' ? value.trim() : value;
}

export function usePluginSettings(page: SettingsSection['page']) {
  const sections = computed(() =>
    plugins().flatMap((plugin) => plugin.settings ?? []).filter((section) => section.page === page));
  const draft = reactive<Record<string, string>>({});
  // What is stored for each secret, as the server masked it.
  const storedMasks = reactive<Record<string, string>>({});

  /** Every control on the page, with the section it is drawn in. */
  function* controls(): Generator<{ section: SettingsSection; control: Control }> {
    for (const section of sections.value) {
      for (const control of section.controls) yield { section, control };
    }
  }

  /**
   * Fills the draft from what the server sent. Secrets start empty.
   *
   * Only the keys the server sent: it leaves out the ones this account may
   * not write — a plugin can keep where its service lives to a super
   * administrator — and a key in the draft is a key the save sends, so one
   * the account could not see would have the whole page's save refused.
   */
  function load(values: Record<string, string>): void {
    for (const { section, control } of controls()) {
      if (!(control.key in values)) {
        delete draft[control.key];
        delete storedMasks[control.key];
        continue;
      }
      if (control.kind === 'secret') {
        draft[control.key] = '';
        storedMasks[control.key] = values[control.key] ?? '';
      } else {
        draft[control.key] = values[control.key] ?? section.defaults[control.key] ?? '';
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
    for (const { control } of controls()) {
      if (!(control.key in draft)) continue;
      out[control.key] = outgoing(control.kind, draft[control.key] ?? '');
    }
    return out;
  }

  /**
   * After a save, only the keys it sent take what the server now holds. Loading
   * the whole response again would also overwrite what was typed while the
   * request was in flight: that edit is not in the payload, so it stays in the
   * draft, unsaved.
   *
   * Returns the baseline for the sent keys. A secret's baseline is the empty
   * draft that collect() reads as "keep what is stored", as load() leaves it.
   */
  function settle(sent: Record<string, string>, saved: Record<string, string>): Record<string, string> {
    const baseline: Record<string, string> = {};
    for (const { control } of controls()) {
      const key = control.key;
      if (!(key in sent)) continue;
      const typed = outgoing(control.kind, draft[key] ?? '');
      if (control.kind === 'secret') {
        storedMasks[key] = saved[key] ?? '';
        if (typed === sent[key]) draft[key] = '';
        baseline[key] = '';
        continue;
      }
      const now = saved[key] ?? sent[key] ?? '';
      if (typed === sent[key]) draft[key] = now;
      baseline[key] = now;
    }
    return baseline;
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

  return { sections, draft, storedMasks, load, collect, settle, withSections, withColumns, words };
}
