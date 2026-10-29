<script setup lang="ts">
import { ref } from 'vue';
import { IconChevron } from '@/icons';
import { t } from '@/composables/useI18n';

const props = defineProps<{ id: string; text: string }>();
const open = ref(false);
</script>

<template>
  <div class="oa-assistant-result ai-thinking" :class="{ open }">
    <button type="button" class="oa-assistant-result-toggle ai-thinking-head" :aria-expanded="open" :aria-controls="`rune-result-${props.id}`" @click="open = !open">
      <IconChevron class="oa-assistant-result-chevron" :size="14" />
      <span>{{ t('assistantResult') }}</span>
    </button>
    <div :id="`rune-result-${props.id}`" class="ai-thinking-content" :aria-hidden="!open" :inert="!open">
      <div class="oa-assistant-result-body"><pre class="oa-assistant-output">{{ props.text }}</pre></div>
    </div>
  </div>
</template>
