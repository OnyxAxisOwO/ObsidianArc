<script setup lang="ts">
// The button that runs a plugin's action, drawn the same on an action card and
// on an action of an opened record. An action that asks first, or is dangerous,
// is armed in place by OaConfirmButton; any other is a plain button.

import OaConfirmButton from '@/components/OaConfirmButton.vue';

defineProps<{
  label: string;
  armedLabel: string;
  armedTitle: string;
  /** Whether the action asks before it runs. */
  asks: boolean;
  danger: boolean;
  disabled: boolean;
}>();

const emit = defineEmits<{ (event: 'run'): void }>();
</script>

<template>
  <OaConfirmButton
    v-if="asks"
    class="oa-btn"
    :class="danger ? 'oa-btn-danger' : 'primary'"
    :disabled="disabled"
    :label="label"
    :armed-label="armedLabel"
    :armed-title="armedTitle"
    @confirm="emit('run')"
  />
  <button
    v-else
    type="button"
    class="oa-btn"
    :class="danger ? 'oa-btn-danger' : 'primary'"
    :disabled="disabled"
    @click="emit('run')"
  >
    {{ label }}
  </button>
</template>
