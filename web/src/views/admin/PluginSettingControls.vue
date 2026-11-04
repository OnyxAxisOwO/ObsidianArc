<script setup lang="ts">
// The controls of one plugin settings section, drawn with the backoffice's
// own fields — inside a card on a page, or bare inside the install panel,
// which asks for the same values before there is a page to ask on.
//
// The caller owns the values and writes them where it saves from; this
// component writes into `draft` in place and keeps nothing of its own.

import { computed } from 'vue';
import OaSelectField from '@/components/OaSelectField.vue';
import OaSwitchField from '@/components/OaSwitchField.vue';
import OaTextField from '@/components/OaTextField.vue';
import type { SettingControl } from '@/plugins/types';

const props = defineProps<{
  controls: SettingControl[];
  /** The values being edited, by key. Secrets are empty until typed into. */
  draft: Record<string, string>;
  /**
   * What is stored for each secret, as the server masked it. Absent on an install,
   * where nothing is stored yet.
   */
  storedMasks?: Record<string, string>;
}>();

// Only the controls the caller has a value for. A page leaves out the keys
// the server did not show this account, which are ones it may not write.
const shown = computed(() => props.controls.filter((control) => control.key in props.draft));

// Every kind draws the same caption: the label, and the help text under it.
function caption(control: SettingControl): { label: string; hint: string | undefined } {
  return { label: control.label(), hint: control.hint?.() };
}

function set(key: string, value: string): void {
  // The caller's reactive object, written in place: the caller is what
  // saves it, and a copy here would be edits it never sees.
  props.draft[key] = value;
}
</script>

<template>
  <div class="oa-plugin-controls">
    <div v-for="control in shown" :key="control.key" class="oa-plugin-control-item">
      <!-- A package names its own labels, so the key is shown too: it is what
           the value is written under, and the install asks for it. -->
      <code class="oa-plugin-control-key">{{ control.key }}</code>
      <OaTextField
        v-if="control.kind === 'text'"
        v-bind="caption(control)"
        :model-value="draft[control.key] ?? ''"
        :placeholder="control.placeholder"
        @update:model-value="set(control.key, $event)"
      />
      <OaTextField
        v-else-if="control.kind === 'secret'"
        v-bind="caption(control)"
        type="password"
        autocomplete="off"
        :model-value="draft[control.key] ?? ''"
        :placeholder="storedMasks?.[control.key] || control.placeholder"
        @update:model-value="set(control.key, $event)"
      />
      <OaSwitchField
        v-else-if="control.kind === 'switch'"
        v-bind="caption(control)"
        :model-value="draft[control.key] === 'true'"
        @update:model-value="set(control.key, String($event))"
      />
      <OaSelectField
        v-else-if="control.kind === 'select'"
        v-bind="caption(control)"
        :model-value="draft[control.key] ?? ''"
        :searchable="false"
        :options="control.options.map((option) => ({ value: option.value, label: option.label() }))"
        @update:model-value="set(control.key, $event)"
      />
    </div>
  </div>
</template>
