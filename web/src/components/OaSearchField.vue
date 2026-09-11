<script setup lang="ts">
import { computed, nextTick, ref } from 'vue';
import { Search } from 'lucide-vue-next';
import OaIconButton from './OaIconButton.vue';
import { IconClose } from '@/icons';
import { t } from '@/composables/useI18n';

const props = defineProps<{ modelValue: string; label: string; collapsible?: boolean }>();
const emit = defineEmits<{ (event: 'update:modelValue', value: string): void }>();
const input = ref<HTMLInputElement | null>(null);
const trigger = ref<{ focus(): void } | null>(null);
const expanded = ref(false);
// Native v-model waits for IME composition, keeping Chinese candidates from
// filtering the list before the reader has committed a word.
const value = computed({
  get: () => props.modelValue,
  set: (next: string) => emit('update:modelValue', next),
});

function clear(): void {
  emit('update:modelValue', '');
  input.value?.focus();
}

function open(): void {
  expanded.value = true;
  void nextTick(() => input.value?.focus());
}

function dismiss(): void {
  emit('update:modelValue', '');
  expanded.value = false;
  void nextTick(() => trigger.value?.focus());
}

function onKeydown(event: KeyboardEvent): void {
  if (event.isComposing) return;
  if (event.key === 'Enter' && props.collapsible) {
    event.preventDefault();
    expanded.value = false;
    void nextTick(() => trigger.value?.focus());
    return;
  }
  if (event.key !== 'Escape') return;
  event.preventDefault();
  event.stopPropagation();
  if (props.collapsible) dismiss();
  else clear();
}
</script>

<template>
  <div
    class="oa-search"
    :class="{ 'oa-search-collapsible': collapsible, expanded: collapsible && expanded }"
  >
    <OaIconButton
      v-if="collapsible"
      ref="trigger"
      class="oa-icon-btn oa-search-open"
      :label="label"
      :aria-expanded="expanded"
      @click="open"
    >
      <Search :size="15" />
    </OaIconButton>
    <Search v-else :size="15" aria-hidden="true" />
    <input
      ref="input"
      v-model="value"
      type="search"
      :aria-label="label"
      :placeholder="label"
      autocomplete="off"
      @keydown="onKeydown"
    >
    <OaIconButton
      v-if="modelValue || (collapsible && expanded)"
      class="oa-icon-btn oa-search-clear"
      :label="t(modelValue ? 'clearSearch' : 'close')"
      @click="collapsible ? dismiss() : clear()"
    >
      <IconClose :size="13" />
    </OaIconButton>
  </div>
</template>
