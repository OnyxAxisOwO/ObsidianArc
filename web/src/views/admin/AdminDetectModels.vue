<script setup lang="ts">
// What a provider says it serves, as rows of the card it sits in.
//
// Two uses. On the provider, rows are ticked and added together (`add`). In
// the model editor, which is already creating exactly one model, a row is
// picked and fills the two fields nobody can guess (`pick`).
//
// The trigger is a row like every other setting — what it does on the left,
// a small button on the right — rather than a button on a line of its own:
// before anything is detected that line is the whole section, and a lone pill
// the width of its label read as something left half-built. The rest of it
// is rows of the same card, so nothing here draws a box inside the box.

import { computed, ref, watch } from 'vue';
import { adminApi } from '@/admin/api';
import { ApiError } from '@/api/client';
import OaRow from '@/components/OaRow.vue';
import OaSearchField from '@/components/OaSearchField.vue';
import { t } from '@/composables/useI18n';
import { IconCheck, IconRefresh } from '@/icons';
import { filterDetected } from '@/lib/detect-filter';

export interface DetectedModel {
  model_id: string;
  display_name: string;
  configured: boolean;
}

const props = defineProps<{
  providerId: string;
  mode: 'add' | 'pick';
  /** Whether this reader may create models; without it `add` is a list to read. */
  canAdd?: boolean;
}>();
const emit = defineEmits<{ (event: 'pick', entry: DetectedModel): void }>();

const detecting = ref(false);
const found = ref<DetectedModel[] | null>(null);
const failure = ref('');
const query = ref('');
const picked = ref<string[]>([]);
const adding = ref(false);

const shown = computed(() => filterDetected(found.value ?? [], query.value));

const meta = computed(() => {
  if (failure.value) return failure.value;
  if (found.value) return t('nModelsFound', { count: found.value.length });
  return props.mode === 'add' ? t('detectModelsAddHint') : t('detectPickHint');
});

function reset(): void {
  found.value = null;
  failure.value = '';
  query.value = '';
  picked.value = [];
}

// A list belongs to the provider it was asked of; the model editor can switch
// providers under it.
watch(() => props.providerId, reset);

async function detect(): Promise<void> {
  if (!props.providerId || detecting.value) return;
  detecting.value = true;
  reset();
  try {
    const { models } = await adminApi.detect(props.providerId);
    found.value = models;
  } catch (error) {
    failure.value = error instanceof ApiError ? error.message : String(error);
  } finally {
    detecting.value = false;
  }
}

function choose(entry: DetectedModel): void {
  if (props.mode === 'pick') {
    emit('pick', entry);
    reset();
    return;
  }
  if (entry.configured || !props.canAdd) return;
  picked.value = picked.value.includes(entry.model_id)
    ? picked.value.filter((id) => id !== entry.model_id)
    : [...picked.value, entry.model_id];
}

async function addPicked(): Promise<void> {
  const entries = (found.value ?? []).filter((entry) => picked.value.includes(entry.model_id));
  if (!entries.length || adding.value) return;
  adding.value = true;
  failure.value = '';
  try {
    await Promise.all(entries.map((entry) => adminApi.createModel({
      provider_id: props.providerId,
      model_id: entry.model_id,
      display_name: entry.display_name || entry.model_id,
    })));
    for (const entry of entries) entry.configured = true;
    picked.value = [];
  } catch (error) {
    failure.value = error instanceof ApiError ? error.message : String(error);
  } finally {
    adding.value = false;
  }
}
</script>

<template>
  <OaRow :title="t('detectModels')" :meta="meta">
    <button
      type="button"
      class="oa-btn small oa-detect-run"
      :class="{ busy: detecting }"
      :disabled="detecting || !providerId"
      @click="detect"
    >
      <IconRefresh :size="13" />
      {{ detecting ? t('detecting') : found ? t('detectAgain') : t('detectRun') }}
    </button>
  </OaRow>

  <template v-if="found && found.length">
    <OaSearchField v-model="query" :label="t('searchDetected', { count: found.length })" />
    <div class="oa-detect-options" role="listbox" :aria-multiselectable="mode === 'add'">
      <button
        v-for="entry in shown"
        :key="entry.model_id"
        type="button"
        role="option"
        class="oa-detect-option"
        :class="{ picked: picked.includes(entry.model_id) }"
        :aria-selected="picked.includes(entry.model_id)"
        :disabled="mode === 'add' && (entry.configured || !canAdd)"
        @click="choose(entry)"
      >
        <span class="oa-detect-option-text">
          <span class="oa-detect-option-id">{{ entry.model_id }}</span>
          <span v-if="entry.display_name && entry.display_name !== entry.model_id" class="oa-detect-option-name">
            {{ entry.display_name }}
          </span>
        </span>
        <span v-if="entry.configured" class="oa-detect-known">{{ t('alreadyAdded') }}</span>
        <span v-else-if="mode === 'add'" class="oa-detect-tick" aria-hidden="true"><IconCheck :size="11" /></span>
      </button>
      <p v-if="!shown.length" class="oa-detect-none">{{ t('noMatches') }}</p>
    </div>
    <!-- The commit for the ticks, only while there are some: an "add" with
         nothing to add is a button that has to be explained. -->
    <OaRow v-if="mode === 'add' && picked.length" :meta="t('detectPicked', { count: picked.length })">
      <button type="button" class="oa-btn small" :disabled="adding" @click="picked = []">{{ t('clearSelection') }}</button>
      <button type="button" class="oa-btn small primary" :disabled="adding" @click="addPicked">
        {{ adding ? t('adding') : t('addPickedN', { count: picked.length }) }}
      </button>
    </OaRow>
  </template>
</template>
