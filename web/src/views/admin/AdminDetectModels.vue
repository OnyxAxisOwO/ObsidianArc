<script setup lang="ts">
// What a provider says it serves, as rows of the card it sits in.
//
// Two uses. On the provider, rows are ticked and added together with the
// defaults (`add`). In the model editor rows are ticked too, and the editor
// reads the ticks (`pick`, through v-model:picked): one fills the two fields
// nobody can guess, several become that many models with the settings the
// form holds.
//
// The trigger is a row like every other setting — what it does on the left,
// a small button on the right — rather than a button on a line of its own:
// before anything is detected that line is the whole section, and a lone pill
// the width of its label read as something left half-built. The rest of it
// is rows of the same card, so nothing here draws a box inside the box.

import { computed, ref, watch } from 'vue';
import { adminApi } from '@/admin/api';
import OaRow from '@/components/OaRow.vue';
import OaSearchField from '@/components/OaSearchField.vue';
import { t } from '@/composables/useI18n';
import { IconCheck, IconRefresh } from '@/icons';
import { filterDetected } from '@/lib/detect-filter';
import { failureText } from './shared';

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
/**
 * The ticked rows. Bound by the model editor; on the provider nobody binds it
 * and it is this component's own state.
 */
const picked = defineModel<DetectedModel[]>('picked', { default: () => [] });

const detecting = ref(false);
const found = ref<DetectedModel[] | null>(null);
const failure = ref('');
const query = ref('');
const adding = ref(false);

const pickedIDs = computed(() => picked.value.map((entry) => entry.model_id));

const shown = computed(() => filterDetected(found.value ?? [], query.value));

const meta = computed(() => {
  if (failure.value) return failure.value;
  if (found.value && props.mode === 'pick' && picked.value.length) return t('detectPicked', { count: picked.value.length });
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
  const asked = props.providerId;
  if (!asked || detecting.value) return;
  detecting.value = true;
  reset();
  try {
    const { models } = await adminApi.detect(asked);
    // The editor can switch providers while this is in flight. The list is the
    // old provider's, and rows ticked from it would be created under the new one.
    if (asked !== props.providerId) return;
    found.value = models;
  } catch (error) {
    if (asked !== props.providerId) return;
    failure.value = reasonText(error);
  } finally {
    detecting.value = false;
  }
}

// Only a code the interface has a sentence for is translated; any other keeps
// the server's sentence, which an administrator may need verbatim.
function reasonText(reason: unknown): string {
  return failureText(reason);
}

// An upstream model already configured can still be picked in the editor: a
// second entry for it, with other settings, is a thing people make on purpose.
function choose(entry: DetectedModel): void {
  if (props.mode === 'add' && (entry.configured || !props.canAdd)) return;
  picked.value = pickedIDs.value.includes(entry.model_id)
    ? picked.value.filter((chosen) => chosen.model_id !== entry.model_id)
    : [...picked.value, entry];
}

async function addPicked(): Promise<void> {
  const entries = picked.value;
  if (!entries.length || adding.value) return;
  const forProvider = props.providerId;
  adding.value = true;
  failure.value = '';
  try {
    // Settled, not all: a row that was created must stop being ticked, or
    // pressing add again creates it a second time. The rows that failed stay
    // ticked so they can be retried.
    const results = await Promise.allSettled(entries.map((entry) => adminApi.createModel({
      provider_id: forProvider,
      model_id: entry.model_id,
      display_name: entry.display_name || entry.model_id,
    })));
    const failed: DetectedModel[] = [];
    results.forEach((result, index) => {
      const entry = entries[index]!;
      if (result.status === 'fulfilled') entry.configured = true;
      else failed.push(entry);
    });
    // The editor may have switched providers meanwhile, and its ticks were
    // cleared for the new one; the failed rows are not for it.
    if (forProvider !== props.providerId) return;
    picked.value = failed;
    // Each refusal names its model. The first alone sends the operator back to
    // the provider with nothing to look for when several rows did not make it.
    failure.value = results.flatMap((result, index) => (result.status === 'rejected'
      ? [`${entries[index]!.model_id}: ${reasonText(result.reason)}`]
      : [])).join('; ');
  } catch (error) {
    failure.value = reasonText(error);
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
    <div class="oa-detect-options" role="listbox" aria-multiselectable="true">
      <button
        v-for="entry in shown"
        :key="entry.model_id"
        type="button"
        role="option"
        class="oa-detect-option"
        :class="{ picked: pickedIDs.includes(entry.model_id) }"
        :aria-selected="pickedIDs.includes(entry.model_id)"
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
        <span v-if="!entry.configured || mode === 'pick'" class="oa-detect-tick" aria-hidden="true"><IconCheck :size="11" /></span>
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
