<script setup lang="ts">
// One record of a plugin's list, opened (AdminListSpec.detail): its fields,
// its pictures, and what can be done to it, in a panel beside the table the
// way the feedback page opens a report.
//
// Each action carries its own inputs and its own button rather than sharing
// the panel's footer: a review that can either reward or refuse is two
// decisions with different questions, and one footer would have to pretend
// they are one.

import { computed, ref } from 'vue';
import { ApiError } from '@/api/client';
import { maskUser } from '@/admin/safeMode';
import OaBadge from '@/components/OaBadge.vue';
import OaConfirmButton from '@/components/OaConfirmButton.vue';
import OaImageLightbox from '@/components/OaImageLightbox.vue';
import OaPanel from '@/components/OaPanel.vue';
import OaSelectField from '@/components/OaSelectField.vue';
import OaTextField from '@/components/OaTextField.vue';
import { t } from '@/composables/useI18n';
import type { RecordAction, RecordDetail } from '@/plugins/types';

const props = defineProps<{ detail: RecordDetail }>();

const emit = defineEmits<{
  (event: 'close'): void;
  (event: 'done'): void;
}>();

const drafts = ref<Record<string, Record<string, string>>>(Object.fromEntries(
  (props.detail.actions ?? []).map((action) => [action.id, { ...(action.defaults ?? {}) }])));
const busy = ref('');
const result = ref<{ message: string; failed: boolean } | null>(null);
const viewing = ref('');

const fields = computed(() => props.detail.fields.map((field) => ({
  ...field,
  value: field.mask ? maskUser(field.value) : field.value,
  href: field.link && /^https?:\/\//i.test(field.value) ? field.value : '',
})));

function set(action: string, key: string, value: string): void {
  drafts.value[action] = { ...drafts.value[action], [key]: value };
}

async function run(action: RecordAction): Promise<void> {
  if (busy.value) return;
  busy.value = action.id;
  result.value = null;
  try {
    result.value = { message: await action.run(drafts.value[action.id] ?? {}), failed: false };
    emit('done');
  } catch (failure) {
    result.value = { message: failure instanceof ApiError ? failure.message : String(failure), failed: true };
  } finally {
    busy.value = '';
  }
}
</script>

<template>
  <OaPanel
    :title="detail.title"
    :footer="false"
    :width="520"
    :error="result?.failed ? result.message : ''"
    @close="emit('close')"
  >
    <div v-if="detail.badges?.length" class="oa-feedback-detail-badges">
      <OaBadge v-for="(badge, index) in detail.badges" :key="index" :tone="badge.tone">{{ badge.label }}</OaBadge>
    </div>

    <dl class="oa-plugin-record">
      <template v-for="(field, index) in fields" :key="index">
        <dt>{{ field.label }}</dt>
        <!-- rel="noreferrer": the address is what somebody typed into a form,
             and the backoffice's own URL is not something to hand it. -->
        <dd :class="{ multiline: field.multiline }">
          <a v-if="field.href" :href="field.href" target="_blank" rel="noreferrer noopener">{{ field.value }}</a>
          <template v-else>{{ field.value }}</template>
        </dd>
      </template>
    </dl>

    <div v-if="detail.images?.length" class="oa-plugin-images">
      <span v-for="url in detail.images" :key="url" class="oa-plugin-image">
        <img :src="url" alt="" loading="lazy" @click="viewing = url">
      </span>
    </div>

    <section v-for="action in detail.actions ?? []" :key="action.id" class="oa-plugin-controls">
      <template v-for="control in action.controls ?? []" :key="control.key">
        <OaSelectField
          v-if="control.kind === 'select'"
          :model-value="drafts[action.id]?.[control.key] ?? ''"
          :label="control.label()"
          :hint="control.hint?.()"
          :searchable="false"
          :options="control.options.map((option) => ({ value: option.value, label: option.label() }))"
          @update:model-value="set(action.id, control.key, $event)"
        />
        <OaTextField
          v-else
          :model-value="drafts[action.id]?.[control.key] ?? ''"
          :label="control.label()"
          :hint="control.hint?.()"
          :type="control.kind === 'datetime' ? 'datetime-local' : 'text'"
          :placeholder="control.kind === 'text' ? control.placeholder : undefined"
          @update:model-value="set(action.id, control.key, $event)"
        />
      </template>
      <div class="oa-card-actions">
        <OaConfirmButton
          v-if="action.confirm"
          class="oa-btn"
          :class="action.danger ? 'danger' : 'primary'"
          :disabled="!!busy"
          :label="busy === action.id ? t('loading') : action.label"
          :armed-label="action.label"
          :armed-title="action.confirm(drafts[action.id] ?? {})"
          @confirm="run(action)"
        />
        <button
          v-else
          type="button"
          class="oa-btn"
          :class="action.danger ? 'danger' : 'primary'"
          :disabled="!!busy"
          @click="run(action)"
        >{{ busy === action.id ? t('loading') : action.label }}</button>
      </div>
    </section>

    <p v-if="result && !result.failed" class="oa-feedback-sent" role="status">{{ result.message }}</p>
  </OaPanel>

  <OaImageLightbox v-if="viewing" :src="viewing" @close="viewing = ''" />
</template>
