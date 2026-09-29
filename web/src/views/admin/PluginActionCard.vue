<script setup lang="ts">
// An action card declared by a plugin, drawn with the backoffice's own controls.
//
// Unlike settings cards which edit persistent key-value configuration, an action
// card holds parameters for an operation (such as a mass card grant), validates
// them, and triggers execution through an action button with optional confirmation.

import { computed, ref } from 'vue';
import { ApiError } from '@/api/client';
import OaConfirmButton from '@/components/OaConfirmButton.vue';
import OaSelectField from '@/components/OaSelectField.vue';
import OaTextField from '@/components/OaTextField.vue';
import { t } from '@/composables/useI18n';
import { IconSpark } from '@/icons';
import type { ActionCardSpec } from '@/plugins/types';
import AdminControlCard from './AdminControlCard.vue';
import ExpiryPresets from './ExpiryPresets.vue';

const props = defineProps<{
  card: ActionCardSpec;
}>();

const emit = defineEmits<{
  (event: 'done'): void;
}>();

const draft = ref<Record<string, string>>({ ...(props.card.defaults ?? {}) });
const busy = ref(false);
const result = ref<{ message: string; isError?: boolean } | null>(null);

function defaultExpiry(): string {
  const date = new Date();
  date.setDate(date.getDate() + 7);
  return new Date(date.getTime() - date.getTimezoneOffset() * 60_000).toISOString().slice(0, 16);
}

// Initialise any unset datetime controls to a sensible default (7 days).
for (const control of props.card.controls) {
  if (control.kind === 'datetime' && !draft.value[control.key]) {
    draft.value[control.key] = defaultExpiry();
  }
}

function set(key: string, value: string): void {
  draft.value[key] = value;
}

const valid = computed(() => {
  for (const control of props.card.controls) {
    if (control.kind === 'text' && control.required && !draft.value[control.key]?.trim()) {
      return false;
    }
    if (control.kind === 'datetime' && control.required && !draft.value[control.key]) {
      return false;
    }
  }
  return true;
});

async function runAction(): Promise<void> {
  busy.value = true;
  result.value = null;
  try {
    const feedback = await props.card.button.run(draft.value);
    result.value = { message: feedback, isError: false };
    emit('done');
  } catch (failure: unknown) {
    const msg = failure instanceof ApiError ? failure.message : String(failure);
    result.value = { message: msg, isError: true };
  } finally {
    busy.value = false;
  }
}
</script>

<template>
  <AdminControlCard
    :id="card.id"
    :title="card.title()"
    :hint="card.hint?.() ?? ''"
    :icon="card.icon ?? IconSpark"
  >
    <div class="oa-plugin-controls">
      <div v-for="control in card.controls" :key="control.key" class="oa-plugin-control-item">
        <OaTextField
          v-if="control.kind === 'text'"
          :model-value="draft[control.key] ?? ''"
          :label="control.label()"
          :hint="control.hint?.()"
          :placeholder="control.placeholder"
          :required="Boolean(control.required)"
          @update:model-value="set(control.key, $event)"
        />
        <OaSelectField
          v-else-if="control.kind === 'select'"
          :model-value="draft[control.key] ?? ''"
          :label="control.label()"
          :hint="control.hint?.()"
          :searchable="false"
          :options="(control.options ?? []).map((opt) => ({ value: opt.value, label: opt.label() }))"
          @update:model-value="set(control.key, $event)"
        />
        <template v-else-if="control.kind === 'datetime'">
          <OaTextField
            :model-value="draft[control.key] ?? ''"
            :label="control.label()"
            :hint="control.hint?.()"
            type="datetime-local"
            :required="Boolean(control.required)"
            @update:model-value="set(control.key, $event)"
          />
          <ExpiryPresets v-if="control.presets" @pick="set(control.key, $event)" />
        </template>
      </div>

      <div class="oa-card-actions" style="margin-top: 14px;">
        <OaConfirmButton
          v-if="card.button.confirm"
          class="oa-btn"
          :class="card.button.danger ? 'danger' : 'primary'"
          :disabled="busy || !valid"
          :label="busy ? t('loading') : card.button.label()"
          :armed-label="card.button.armedLabel ? card.button.armedLabel() : card.button.label()"
          :armed-title="card.button.confirm(draft)"
          @confirm="runAction"
        />
        <button
          v-else
          type="button"
          class="oa-btn"
          :class="card.button.danger ? 'danger' : 'primary'"
          :disabled="busy || !valid"
          @click="runAction"
        >
          {{ busy ? t('loading') : card.button.label() }}
        </button>
      </div>

      <p
        v-if="result"
        class="oa-field-hint"
        :style="{ color: result.isError ? 'var(--ai-danger, #ef4444)' : 'var(--ai-accent, #10b981)' }"
        role="status"
      >
        {{ result.message }}
      </p>
    </div>
  </AdminControlCard>
</template>
