<script setup lang="ts">
// One card of a plugin's settings, drawn with the page's own controls.
//
// The page owns the values: it loads the section's keys with its own, keeps
// them in `draft`, and sends them with its own save — so a plugin's settings
// dirty, save and fail exactly like the card beside them. This component is
// only the layout the plugin declared.

import OaSelectField from '@/components/OaSelectField.vue';
import OaSwitchField from '@/components/OaSwitchField.vue';
import OaTextField from '@/components/OaTextField.vue';
import { IconSliders } from '@/icons';
import type { SettingsSection } from '@/plugins/types';
import AdminControlCard from './AdminControlCard.vue';

const props = defineProps<{
  section: SettingsSection;
  /** The values being edited, by key. Secrets are empty until typed into. */
  draft: Record<string, string>;
  /** What is stored for each secret, as the server masked it. */
  hints: Record<string, string>;
}>();

function set(key: string, value: string): void {
  // The page's own reactive object, written in place: the page is what saves
  // it, and a copy here would be edits it never sees.
  props.draft[key] = value;
}
</script>

<template>
  <AdminControlCard :id="section.id" :title="section.title()" :hint="section.hint?.() ?? ''" :icon="section.icon ?? IconSliders">
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
  </AdminControlCard>
</template>
