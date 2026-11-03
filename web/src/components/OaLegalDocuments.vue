<script setup lang="ts">
import { watch } from 'vue';
import type { LegalDocument } from '@/api/auth';
import OaMarkdown from './OaMarkdown.vue';
import { t } from '@/composables/useI18n';

const props = defineProps<{
  documents: LegalDocument[];
  modelValue: Record<string, boolean>;
  readonly?: boolean;
}>();
const emit = defineEmits<{ (event: 'update:modelValue', value: Record<string, boolean>): void }>();

// Consent to a draft does not carry over to a new text loaded in this form.
watch(() => props.documents, () => emit('update:modelValue', {}));
function title(document: LegalDocument): string {
  return document.title || t(document.id === 'privacy' ? 'privacyPolicy' : 'userAgreement');
}
</script>

<template>
  <div v-if="props.documents.length" class="oa-legal-documents">
    <div v-for="document in props.documents" :key="document.id" class="oa-legal-document">
      <details>
        <summary>{{ title(document) }}</summary>
        <OaMarkdown class="ai-answer oa-legal-body" :text="document.body" />
      </details>
      <label v-if="document.required && !props.readonly" class="oa-checkbox-field">
        <input
          type="checkbox"
          :checked="!!props.modelValue[document.id]"
          required
          @change="emit('update:modelValue', { ...props.modelValue, [document.id]: ($event.target as HTMLInputElement).checked })"
        >
        <span>{{ t('legalAgree', { title: title(document) }) }}</span>
      </label>
    </div>
  </div>
</template>
