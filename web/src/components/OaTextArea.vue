<script setup lang="ts">
import { computed, ref } from 'vue';
import OaField from './OaField.vue';
import { t } from '@/composables/useI18n';

const props = withDefaults(defineProps<{
  modelValue: string;
  label: string;
  hint?: string | undefined;
  placeholder?: string | undefined;
  rows?: number;
  /**
   * The most the value may take, in UTF-8 bytes, when the server has a limit
   * it refuses past. A counter says how much is left, or how far over it is.
   */
  maxBytes?: number | undefined;
  /**
   * The most the field takes, in UTF-16 units, as the native attribute counts
   * them. Units are never fewer than code points, so a server counting code
   * points cannot refuse what the field let through; characters outside the BMP
   * take two units and reach the limit a little sooner.
   */
  maxLength?: number | undefined;
}>(), { rows: 3, maxBytes: undefined, maxLength: undefined });

defineEmits<{ (event: 'update:modelValue', value: string): void }>();

const area = ref<HTMLTextAreaElement | null>(null);
defineExpose({ focus: (options?: FocusOptions) => area.value?.focus(options) });

// Bytes, not characters: that is what the server counts, and a CJK character
// is three of them.
const usedBytes = computed(() => new TextEncoder().encode(props.modelValue).length);
const overLimit = computed(() => props.maxBytes !== undefined && usedBytes.value > props.maxBytes);
</script>

<template>
  <OaField :label="props.label" :hint="props.hint">
    <textarea
      ref="area"
      :rows="props.rows"
      spellcheck="false"
      :value="props.modelValue"
      :placeholder="props.placeholder"
      :maxlength="props.maxLength"
      :aria-invalid="overLimit || undefined"
      @input="$emit('update:modelValue', ($event.target as HTMLTextAreaElement).value)"
    />
    <template v-if="props.maxBytes !== undefined" #after>
      <span class="oa-field-hint">{{ overLimit
        ? t('fieldByteCountOver', { used: usedBytes, limit: props.maxBytes })
        : t('fieldByteCount', { used: usedBytes, limit: props.maxBytes }) }}</span>
    </template>
  </OaField>
</template>
