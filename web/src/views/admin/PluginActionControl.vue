<script setup lang="ts">
// One input a plugin declared for an operation (ActionControl), drawn with the
// backoffice's own controls. Shared by the action cards and the actions on an
// opened record, so a control a plugin can ask for in one place it can ask
// for in the other, and looks the same in both.
//
// Every value is a string, as the draft holds it: a switch is 'true' or
// 'false', a list of rows is the rows as JSON. The plugin parses what it
// declared; the core only keeps the draft.

import { computed, onMounted, ref } from 'vue';
import OaSelect from '@/components/OaSelect.vue';
import OaSelectField from '@/components/OaSelectField.vue';
import OaSwitchField from '@/components/OaSwitchField.vue';
import OaTextArea from '@/components/OaTextArea.vue';
import OaTextField from '@/components/OaTextField.vue';
import { t } from '@/composables/useI18n';
import { IconClose } from '@/icons';
import type { ActionControl, ActionDraft, RowColumn } from '@/plugins/types';
import ExpiryPresets from './ExpiryPresets.vue';

const props = defineProps<{ control: ActionControl; draft: ActionDraft }>();
const emit = defineEmits<{ (event: 'set', key: string, value: string): void }>();

const value = computed(() => props.draft[props.control.key] ?? '');

function set(next: string): void {
  emit('set', props.control.key, next);
}

const loaded = ref<Array<{ value: string; label: string }>>([]);
const options = computed(() => {
  if (props.control.kind !== 'select') return [];
  return [
    ...props.control.options.map((option) => ({ value: option.value, label: option.label() })),
    ...loaded.value,
  ];
});

onMounted(async () => {
  if (props.control.kind !== 'select' || !props.control.load) return;
  try {
    loaded.value = await props.control.load();
  } catch {
    // The fixed options are still there to pick from; a list the server
    // would not give is not a reason to draw no control at all.
    loaded.value = [];
  }
});

type Row = Record<string, string>;

const rows = computed<Row[]>(() => {
  if (props.control.kind !== 'rows') return [];
  try {
    const parsed: unknown = JSON.parse(value.value || '[]');
    return Array.isArray(parsed) ? parsed.filter((row): row is Row => !!row && typeof row === 'object') : [];
  } catch {
    return [];
  }
});

const columns = computed<RowColumn[]>(() => {
  if (props.control.kind !== 'rows') return [];
  return props.control.columns.filter((column) => !column.visible || column.visible(props.draft));
});

function blankRow(): Row {
  const row: Row = {};
  if (props.control.kind !== 'rows') return row;
  for (const column of props.control.columns) {
    row[column.key] = column.kind === 'select' ? column.options[0]?.value ?? ''
      : column.kind === 'switch' ? 'false' : '';
  }
  return row;
}

function writeRows(next: Row[]): void {
  set(JSON.stringify(next));
}

function setCell(index: number, key: string, cell: string): void {
  writeRows(rows.value.map((row, at) => (at === index ? { ...row, [key]: cell } : row)));
}

const canAdd = computed(() => props.control.kind === 'rows' && (props.control.max === undefined || rows.value.length < props.control.max));
const canRemove = computed(() => props.control.kind === 'rows' && rows.value.length > (props.control.min ?? 0));
</script>

<template>
  <OaTextField
    v-if="control.kind === 'text'"
    :model-value="value"
    :label="control.label()"
    :hint="control.hint?.()"
    :placeholder="control.placeholder"
    :required="Boolean(control.required)"
    @update:model-value="set"
  />
  <OaTextArea
    v-else-if="control.kind === 'textarea'"
    :model-value="value"
    :label="control.label()"
    :hint="control.hint?.()"
    :placeholder="control.placeholder"
    :rows="control.rows ?? 4"
    @update:model-value="set"
  />
  <OaTextField
    v-else-if="control.kind === 'number'"
    :model-value="value"
    :label="control.label()"
    :hint="control.hint?.()"
    type="number"
    :required="Boolean(control.required)"
    @update:model-value="set"
  />
  <OaSwitchField
    v-else-if="control.kind === 'switch'"
    :model-value="value === 'true'"
    :label="control.label()"
    :hint="control.hint?.()"
    @update:model-value="set($event ? 'true' : 'false')"
  />
  <OaSelectField
    v-else-if="control.kind === 'select'"
    :model-value="value"
    :label="control.label()"
    :hint="control.hint?.()"
    :searchable="options.length > 8"
    :options="options"
    @update:model-value="set"
  />
  <template v-else-if="control.kind === 'datetime'">
    <OaTextField
      :model-value="value"
      :label="control.label()"
      :hint="control.hint?.()"
      type="datetime-local"
      :required="Boolean(control.required)"
      @update:model-value="set"
    />
    <ExpiryPresets v-if="control.presets" @pick="set" />
  </template>
  <div v-else-if="control.kind === 'rows'" class="oa-field">
    <span class="oa-field-label">{{ control.label() }}</span>
    <div class="oa-plugin-rows">
      <div v-for="(row, index) in rows" :key="index" class="oa-plugin-row">
        <span class="oa-plugin-row-index">{{ index + 1 }}</span>
        <div class="oa-plugin-row-cells">
          <template v-for="column in columns" :key="column.key">
            <label v-if="column.kind === 'switch'" class="oa-checkbox-field oa-plugin-row-check">
              <input
                type="checkbox"
                :checked="row[column.key] === 'true'"
                @change="setCell(index, column.key, ($event.target as HTMLInputElement).checked ? 'true' : 'false')"
              >
              <span>{{ column.label() }}</span>
            </label>
            <label v-else class="oa-plugin-row-cell" :class="{ wide: column.kind === 'text' }">
              <span class="oa-plugin-row-label">{{ column.label() }}</span>
              <OaSelect
                v-if="column.kind === 'select'"
                :model-value="row[column.key] ?? ''"
                :aria-label="column.label()"
                :searchable="false"
                :choices="column.options.map((option) => ({ value: option.value, label: option.label() }))"
                @update:model-value="setCell(index, column.key, $event)"
              />
              <input
                v-else
                class="oa-field-input"
                :type="column.kind === 'number' ? 'number' : 'text'"
                :min="column.kind === 'number' ? column.min : undefined"
                :max="column.kind === 'number' ? column.max : undefined"
                :step="column.kind === 'number' ? column.step ?? 'any' : undefined"
                :placeholder="column.kind === 'text' ? column.placeholder : undefined"
                :value="row[column.key] ?? ''"
                @input="setCell(index, column.key, ($event.target as HTMLInputElement).value)"
              >
            </label>
          </template>
        </div>
        <button
          v-if="canRemove"
          type="button"
          class="oa-icon-btn"
          :title="t('pluginRowRemove')"
          @click="writeRows(rows.filter((_, at) => at !== index))"
        >
          <IconClose :size="13" />
        </button>
      </div>
      <button v-if="canAdd" type="button" class="oa-btn oa-plugin-rows-add" @click="writeRows([...rows, blankRow()])">
        {{ control.add() }}
      </button>
    </div>
    <span v-if="control.hint" class="oa-field-hint">{{ control.hint() }}</span>
  </div>
</template>
