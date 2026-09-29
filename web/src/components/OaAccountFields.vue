<script setup lang="ts">
// The inputs for the account fields plugins add — see lib/account-fields.ts
// for which ones a form draws. One component for the sign-up card, the
// completion form, the account's own screen and the backoffice, so the four
// cannot disagree about what a field is called or what it accepts.

import OaField from './OaField.vue';
import { fieldSpec } from '@/plugins/registry';
import type { FieldPlan } from '@/lib/account-fields';

// A fragment of fields rather than one element, so each lands in the form's
// own layout; attributes the caller passes (a safe-mode blur, say) go on
// every field instead of on a wrapper that does not exist.
defineOptions({ inheritAttrs: false });

const props = defineProps<{
  modelValue: Record<string, string>;
  plan: FieldPlan;
}>();

const emit = defineEmits<{ (event: 'update:modelValue', value: Record<string, string>): void }>();

function update(key: string, value: string): void {
  emit('update:modelValue', { ...props.modelValue, [key]: value });
}
</script>

<template>
  <template v-for="key in props.plan.keys" :key="key">
    <OaField
      v-if="fieldSpec(key)"
      v-bind="$attrs"
      :label="props.plan.required.includes(key) ? fieldSpec(key)!.label() : fieldSpec(key)!.optionalLabel()"
      :hint="fieldSpec(key)!.hint?.()"
    >
      <input
        type="text"
        spellcheck="false"
        autocomplete="off"
        :name="`field-${key}`"
        :value="props.modelValue[key] ?? ''"
        :placeholder="fieldSpec(key)!.placeholder?.()"
        :maxlength="fieldSpec(key)!.maxLength"
        :inputmode="fieldSpec(key)!.inputMode"
        :required="props.plan.required.includes(key)"
        @input="update(key, ($event.target as HTMLInputElement).value)"
      >
    </OaField>
  </template>
</template>
