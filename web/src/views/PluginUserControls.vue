<script setup lang="ts">
// The controls of a form a plugin puts in front of the people using the
// instance (UserFormControl), drawn with the core's own components. Used by
// a panel's fixed form and by the forms an operator writes, which are the
// same controls arriving later.

import { prepareImage } from '@/chat/image';
import OaSwitchField from '@/components/OaSwitchField.vue';
import OaTextArea from '@/components/OaTextArea.vue';
import OaTextField from '@/components/OaTextField.vue';
import { t } from '@/composables/useI18n';
import { IconCheck, IconClose } from '@/icons';
import type { UserFormControl, UserFormValues } from '@/plugins/types';

const props = defineProps<{ controls: readonly UserFormControl[]; values: UserFormValues }>();
const emit = defineEmits<{
  (event: 'set', key: string, value: string | boolean | string[]): void;
  (event: 'error', message: string): void;
  (event: 'view', url: string): void;
}>();

// Pictures are sent inside the JSON body, which the server caps at 4 MiB for
// a plugin's route; base64 is a third bigger than the bytes it carries.
const MAX_IMAGE_BYTES = 2_800_000;

function text(key: string): string {
  const value = props.values[key];
  return typeof value === 'string' ? value : '';
}

function list(key: string): string[] {
  const value = props.values[key];
  return Array.isArray(value) ? value : [];
}

function toggle(control: Extract<UserFormControl, { kind: 'checks' }>, option: string): void {
  const have = list(control.key);
  if (have.includes(option)) {
    emit('set', control.key, have.filter((value) => value !== option));
    return;
  }
  if (control.max && have.length >= control.max) {
    emit('error', t('pluginChecksTooMany', { count: control.max }));
    return;
  }
  // In the order the options are listed, not the order they were ticked.
  const next = [...have, option];
  emit('set', control.key, control.options.map((o) => o.value).filter((value) => next.includes(value)));
}

function setRow(key: string, index: number, value: string): void {
  emit('set', key, list(key).map((item, at) => (at === index ? value : item)));
}

async function pick(control: Extract<UserFormControl, { kind: 'images' }>, event: Event): Promise<void> {
  const input = event.target as HTMLInputElement;
  const files = Array.from(input.files ?? []);
  input.value = '';
  const have = list(control.key);
  if (have.length + files.length > control.max) {
    emit('error', t('pluginImagesTooMany', { count: control.max }));
    return;
  }
  const added: string[] = [];
  for (const file of files) {
    try {
      const image = await prepareImage(file);
      URL.revokeObjectURL(image.previewURL);
      added.push(`data:${image.mime};base64,${image.data}`);
    } catch (failure) {
      emit('error', failure instanceof Error ? failure.message : t('failed'));
      return;
    }
  }
  const next = [...have, ...added];
  if (next.reduce((sum, url) => sum + url.length, 0) > MAX_IMAGE_BYTES) {
    emit('error', t('pluginImagesTooLarge'));
    return;
  }
  emit('set', control.key, next);
}
</script>

<template>
  <template v-for="control in controls" :key="control.key">
    <div v-if="control.kind === 'choice' && control.list" class="oa-field" role="radiogroup" :aria-label="control.label()">
      <span class="oa-field-label">{{ control.label() }}<span v-if="control.required" class="oa-plugin-required">*</span></span>
      <div class="oa-plugin-options">
        <button
          v-for="option in control.options"
          :key="option.value"
          type="button"
          role="radio"
          class="oa-plugin-option"
          :class="{ active: text(control.key) === option.value }"
          :aria-checked="text(control.key) === option.value"
          @click="emit('set', control.key, option.value)"
        >
          <span>{{ option.label() }}</span>
          <IconCheck v-if="text(control.key) === option.value" :size="14" />
        </button>
      </div>
      <span v-if="control.hint" class="oa-field-hint">{{ control.hint() }}</span>
    </div>

    <div v-else-if="control.kind === 'choice'" class="oa-field">
      <span class="oa-field-label">{{ control.label() }}<span v-if="control.required" class="oa-plugin-required">*</span></span>
      <div class="oa-segmented">
        <button
          v-for="option in control.options"
          :key="option.value"
          type="button"
          class="oa-segmented-option"
          :class="{ active: text(control.key) === option.value }"
          @click="emit('set', control.key, option.value)"
        >{{ option.label() }}</button>
      </div>
      <span v-if="control.hint" class="oa-field-hint">{{ control.hint() }}</span>
    </div>

    <div v-else-if="control.kind === 'checks'" class="oa-field" role="group" :aria-label="control.label()">
      <span class="oa-field-label">{{ control.label() }}<span v-if="control.required" class="oa-plugin-required">*</span></span>
      <div class="oa-plugin-options">
        <button
          v-for="option in control.options"
          :key="option.value"
          type="button"
          role="checkbox"
          class="oa-plugin-option"
          :class="{ active: list(control.key).includes(option.value) }"
          :aria-checked="list(control.key).includes(option.value)"
          @click="toggle(control, option.value)"
        >
          <span>{{ option.label() }}</span>
          <IconCheck v-if="list(control.key).includes(option.value)" :size="14" />
        </button>
      </div>
      <span v-if="control.hint" class="oa-field-hint">{{ control.hint() }}</span>
    </div>

    <OaTextField
      v-else-if="control.kind === 'text'"
      :model-value="text(control.key)"
      :label="control.label()"
      :hint="control.hint?.()"
      :placeholder="control.placeholder?.()"
      :max-length="control.maxLength"
      :required="Boolean(control.required)"
      @update:model-value="emit('set', control.key, $event)"
    />

    <OaTextArea
      v-else-if="control.kind === 'textarea'"
      :model-value="text(control.key)"
      :label="control.label()"
      :hint="control.hint?.()"
      :placeholder="control.placeholder?.()"
      :rows="control.rows ?? 6"
      @update:model-value="emit('set', control.key, $event)"
    />

    <OaSwitchField
      v-else-if="control.kind === 'switch'"
      :model-value="values[control.key] === true"
      :label="control.label()"
      :hint="control.hint?.()"
      @update:model-value="emit('set', control.key, $event)"
    />

    <div v-else-if="control.kind === 'list'" class="oa-field" role="group" :aria-label="control.label()">
      <span class="oa-field-label">{{ control.label() }}<span v-if="control.required" class="oa-plugin-required">*</span></span>
      <div class="oa-plugin-list">
        <div v-for="(item, index) in list(control.key)" :key="index" class="oa-plugin-list-row">
          <input
            class="oa-field-input"
            type="text"
            :value="item"
            :placeholder="control.placeholder?.()"
            :maxlength="control.maxLength"
            :aria-label="`${control.label()} ${index + 1}`"
            @input="setRow(control.key, index, ($event.target as HTMLInputElement).value)"
          >
          <!-- The last row stays: removing it would leave nothing to type
               into, which is the add button's job to undo. -->
          <button
            v-if="list(control.key).length > 1"
            type="button"
            class="oa-icon-btn"
            :title="t('pluginRowRemove')"
            @click="emit('set', control.key, list(control.key).filter((_, at) => at !== index))"
          >
            <IconClose :size="13" />
          </button>
        </div>
        <button
          v-if="!control.max || list(control.key).length < control.max"
          type="button"
          class="oa-btn oa-plugin-rows-add"
          @click="emit('set', control.key, [...list(control.key), ''])"
        >
          {{ control.add() }}
        </button>
      </div>
      <span v-if="control.hint" class="oa-field-hint">{{ control.hint() }}</span>
    </div>

    <div v-else-if="control.kind === 'images'" class="oa-field">
      <span class="oa-field-label">{{ control.label() }}</span>
      <div class="oa-plugin-images">
        <span v-for="(url, index) in list(control.key)" :key="index" class="oa-plugin-image">
          <img :src="url" alt="" @click="emit('view', url)">
          <button
            type="button"
            class="oa-icon-btn"
            :title="t('pluginImagesRemove')"
            @click="emit('set', control.key, list(control.key).filter((_, at) => at !== index))"
          >
            <IconClose :size="12" />
          </button>
        </span>
        <label v-if="list(control.key).length < control.max" class="oa-btn">
          {{ t('pluginImagesAdd') }}
          <input type="file" accept="image/*" multiple hidden @change="pick(control, $event)">
        </label>
      </div>
      <span v-if="control.hint" class="oa-field-hint">{{ control.hint() }}</span>
    </div>
  </template>
</template>
