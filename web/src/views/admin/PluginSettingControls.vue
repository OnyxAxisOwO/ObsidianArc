<script setup lang="ts">
// The controls of one plugin settings section, drawn with the backoffice's
// own fields — inside a card on a page, or bare inside the install panel,
// which asks for the same values before there is a page to ask on.
//
// The caller owns the values and writes them where it saves from; this
// component writes into `draft` in place and keeps nothing of its own.

import OaSelectField from '@/components/OaSelectField.vue';
import OaSwitchField from '@/components/OaSwitchField.vue';
import OaTextField from '@/components/OaTextField.vue';
import type { SettingsSection } from '@/plugins/types';

const props = defineProps<{
  section: SettingsSection;
  /** The values being edited, by key. Secrets are empty until typed into. */
  draft: Record<string, string>;
  /** What is stored for each secret, as the server masked it. */
  hints: Record<string, string>;
}>();

function set(key: string, value: string): void {
  // The caller's reactive object, written in place: the caller is what
  // saves it, and a copy here would be edits it never sees.
  props.draft[key] = value;
}
</script>

<template>
  <template v-for="control in section.controls" :key="control.key">
    <OaTextField
      v-if="control.kind === 'text'"
      :model-value="draft[control.key] ?? ''"
      :label="control.label()"
      :hint="control.hint?.()"
      :placeholder="control.placeholder"
      @update:model-value="set(control.key, $event)"
    />
    <OaTextField
      v-else-if="control.kind === 'secret'"
      type="password"
      autocomplete="off"
      :model-value="draft[control.key] ?? ''"
      :label="control.label()"
      :hint="control.hint?.()"
      :placeholder="hints[control.key] || control.placeholder"
      @update:model-value="set(control.key, $event)"
    />
    <OaSwitchField
      v-else-if="control.kind === 'switch'"
      :model-value="draft[control.key] === 'true'"
      :label="control.label()"
      :hint="control.hint?.()"
      @update:model-value="set(control.key, String($event))"
    />
    <OaSelectField
      v-else-if="control.kind === 'select'"
      :model-value="draft[control.key] ?? ''"
      :label="control.label()"
      :hint="control.hint?.()"
      :searchable="false"
      :options="control.options.map((option) => ({ value: option.value, label: option.label() }))"
      @update:model-value="set(control.key, $event)"
    />
  </template>
</template>
