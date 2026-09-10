<script setup lang="ts">
import { computed, ref } from 'vue';
import { Search } from 'lucide-vue-next';
import OaIconButton from './OaIconButton.vue';
import { IconClose } from '@/icons';
import { t } from '@/composables/useI18n';

const props = defineProps<{ modelValue: string; label: string }>();
const emit = defineEmits<{ (event: 'update:modelValue', value: string): void }>();
const input = ref<HTMLInputElement | null>(null);
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

function onEscape(event: KeyboardEvent): void {
  if (event.isComposing) return;
  event.preventDefault();
  clear();
}
</script>

<template>
  <div class="oa-search">
    <Search :size="15" aria-hidden="true" />
    <input
      ref="input"
      v-model="value"
      type="search"
      :aria-label="label"
      :placeholder="label"
      autocomplete="off"
      @keydown.esc.stop="onEscape"
    >
    <OaIconButton v-if="modelValue" class="oa-icon-btn oa-search-clear" :label="t('clearSearch')" @click="clear">
      <IconClose :size="13" />
    </OaIconButton>
  </div>
</template>
