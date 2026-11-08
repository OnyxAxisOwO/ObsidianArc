<script setup lang="ts">
import { computed, onMounted, ref, watch } from 'vue';
import ModelTile from './ModelTile.vue';
import OaScrollArea from '@/components/OaScrollArea.vue';
import { IconCheck, IconChevron, IconCopy } from '@/icons';
import { t } from '@/composables/useI18n';
import { useLatestTimeout } from '@/composables/useLatestTimeout';
import { copyToClipboard } from '@/lib/clipboard';
import type { AvailableModel } from './useModels';
import '@/styles/_model-info.scss';

const props = defineProps<{ model: AvailableModel }>();
defineEmits<{ close: [] }>();
const back = ref<HTMLButtonElement | null>(null);
// The list is hidden on narrow screens, so its trigger cannot retain focus.
onMounted(() => { back.value?.focus({ preventScroll: true }); });
const copied = ref('');
const copyError = ref(false);
const clearCopy = useLatestTimeout();
watch(() => props.model.id, () => { copied.value = ''; copyError.value = false; });

// Exact counts remain selectable and copyable instead of losing precision to
// the abbreviated figures used in the compact model list.
const specs = computed(() => {
  const model = props.model;
  const can = [
    model.supports_images && t('specVision'),
    model.supports_reasoning && t('specReasoning'),
    model.supports_tools && t('specTools'),
    model.supports_chat_image_gen && t('canImageGen'),
  ].filter(Boolean).join(' · ');
  return [
    { label: t('specContext'), value: model.context_window > 0 ? String(model.context_window) : '' },
    { label: t('specOutput'), value: model.max_output_tokens > 0 ? String(model.max_output_tokens) : '' },
    { label: t('apiNameLabel'), value: model.api_name ?? '', copy: true },
    { label: t('specCan'), value: can },
  ].filter((row) => row.value);
});

async function copy(apiOnly = false): Promise<void> {
  const model = props.model;
  const content = apiOnly ? model.api_name! : [
    model.display_name, model.description,
    ...specs.value.map((row) => `${row.label}: ${row.value}`),
  ].filter(Boolean).join('\n');
  const success = await copyToClipboard(content);
  if (props.model.id !== model.id) return;
  copyError.value = !success;
  if (success) {
    copied.value = apiOnly ? 'api' : 'all';
    clearCopy(() => { copied.value = ''; }, 2000);
  }
}
</script>

<template>
  <div class="ai-model-info-head">
    <button ref="back" type="button" class="ai-pop-back" :aria-label="t('back')" @click="$emit('close')">
      <IconChevron :size="14" />
    </button>
    <span>{{ t('modelInfo') }}</span>
  </div>
  <OaScrollArea wrap-class="ai-model-info-wrap" scroll-class="ai-model-info-content">
    <div class="ai-model-info-title">
      <ModelTile :avatar="model.avatar" :name="model.display_name" />
      <strong>{{ model.display_name }}</strong>
    </div>
    <p v-if="model.description" class="ai-model-info-description">{{ model.description }}</p>
    <p v-if="model.usable === false" class="ai-pop-note">{{ t('modelNotAllowedGroup') }}</p>
    <dl class="ai-model-info-spec">
      <div v-for="row in specs" :key="row.label">
        <dt>{{ row.label }}</dt>
        <dd>
          <span>{{ row.value }}</span>
          <button
            v-if="row.copy" type="button" class="ai-pop-model-info ai-model-info-api-copy"
            :aria-label="`${copied === 'api' ? t('copied') : t('copy')}: ${t('apiNameLabel')}`"
            :title="copied === 'api' ? t('copied') : t('copy')" @click="copy(true)"
          ><IconCheck v-if="copied === 'api'" :size="13" /><IconCopy v-else :size="13" /></button>
        </dd>
      </div>
    </dl>
    <button type="button" class="ai-model-info-copy" aria-live="polite" @click="copy()">
      <IconCheck v-if="copied === 'all'" :size="14" /><IconCopy v-else :size="14" />
      {{ copied === 'all' ? t('copied') : t('modelInfoCopy') }}
    </button>
    <p v-if="copyError" class="ai-pop-note" role="status">{{ t('copyFailed') }}</p>
  </OaScrollArea>
</template>
