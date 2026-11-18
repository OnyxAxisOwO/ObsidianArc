<script setup lang="ts">
// What a new conversation starts with.

import { computed, onMounted, ref } from 'vue';
import { stopsFor, stopFor, type AvailableModel } from '@/chat/useModels';
import { api } from '@/api/client';
import OaField from '@/components/OaField.vue';
import OaGroup from '@/components/OaGroup.vue';
import OaRow from '@/components/OaRow.vue';
import OaSelect from '@/components/OaSelect.vue';
import OaSelectField from '@/components/OaSelectField.vue';
import OaSwitchField from '@/components/OaSwitchField.vue';
import type { Choice } from '@/components/choice';
import { t } from '@/composables/useI18n';
import { currentPreferences, currentUser, syncPreferences } from '@/stores/session';

// The labels are computed from what was loaded rather than written into a
// list once. A list built here would keep the language it was built in: this
// panel stays mounted while the language changes, and nothing would rebuild
// it.
const models = ref<AvailableModel[] | null>(null);
const loadFailed = ref(false);
// The empty value is a real choice, not a prompt to pick one: it means
// whichever model the account can reach first, which is what a new account
// already gets.
const choices = computed<Array<Choice<string>>>(() => {
  if (loadFailed.value) return [{ value: '', label: t('couldNotLoadModels') }];
  return [
    { value: '', label: t('firstAvailableModel') },
    ...(models.value ?? []).map((entry) => ({ value: entry.id, label: entry.display_name })),
  ];
});
const model = ref('');
const stops = computed(() => stopsFor(models.value?.find((entry) => entry.id === model.value) ?? models.value?.[0] ?? null));
const effortChoices = computed(() => stops.value.slice(1).map((stop) => ({ value: stop.effort, label: stop.label })));
const effort = computed({
  get: () => {
    const value = currentPreferences.value['reasoning_effort'];
    return stops.value[stopFor({ enabled: true, effort: typeof value === 'string' ? value : '' }, stops.value)]!.effort;
  },
  set: (value: string) => syncPreferences({ reasoning_effort: value }),
});
const stats = ref(currentPreferences.value['show_stats'] === true);
const sendShortcut = computed({
  get: () => currentPreferences.value['send_on_enter'] === true,
  set: (value: boolean) => syncPreferences({ send_on_enter: value }),
});

// Only offered where the group allows it. Hiding it is not the enforcement —
// the chat checks the same permission before drawing the line — it is so
// nobody is shown a switch that would do nothing.
const statsAllowed = computed(() => currentUser.value?.allow_stats !== false);

// Fetched here rather than read from the shared model list: that list is loaded
// once at startup and keeps no record of a failed load, and this section has to
// say when the catalogue could not be read.
onMounted(() => {
  void api.get<{ models: AvailableModel[] }>('/api/models')
    .then(({ models: list }) => {
      const usable = list.filter((entry) => entry.usable !== false);
      models.value = usable;
      // Only if it is still on the list: a model that has since been withdrawn
      // would otherwise leave the control naming nothing.
      const stored = currentPreferences.value['default_model_id'];
      if (typeof stored === 'string' && usable.some((entry) => entry.id === stored)) {
        model.value = stored;
      }
    })
    .catch(() => {
      loadFailed.value = true;
    });
});
</script>

<template>
  <OaGroup :title="t('secChatDefaults')" :hint="t('chatDefaultsHint')">
    <OaRow stacked>
      <OaSwitchField
        v-model="sendShortcut"
        :label="t('sendShortcut')"
      />
    </OaRow>

    <OaRow stacked>
      <OaField :label="t('defaultModel')">
        <OaSelect
          v-model="model"
          :choices="choices"
          @update:model-value="syncPreferences({ default_model_id: $event })"
        />
      </OaField>
    </OaRow>

    <OaRow stacked>
      <OaSelectField
        v-model="effort"
        :label="t('defaultEffort')"
        :hint="t('defaultEffortHint')"
        :options="effortChoices"
      />
    </OaRow>

    <OaRow v-if="statsAllowed" stacked>
      <OaSwitchField
        v-model="stats"
        :label="t('showStats')"
        :hint="t('showStatsHint')"
        @update:model-value="syncPreferences({ show_stats: $event })"
      />
    </OaRow>
  </OaGroup>
</template>
