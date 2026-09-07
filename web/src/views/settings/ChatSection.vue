<script setup lang="ts">
// What a new conversation starts with.

import { computed, onMounted, ref } from 'vue';
import { api } from '@/api/client';
import OaField from '@/components/OaField.vue';
import OaSelect from '@/components/OaSelect.vue';
import OaSelectField from '@/components/OaSelectField.vue';
import OaSwitchField from '@/components/OaSwitchField.vue';
import type { Choice } from '@/components/choice';
import { t } from '@/composables/useI18n';
import { currentPreferences, currentUser, syncPreferences } from '@/stores/session';

// The empty value is a real choice, not a prompt to pick one: it means
// whichever model the account can reach first, which is what a new account
// already gets.
const choices = ref<Array<Choice<string>>>([{ value: '', label: t('firstAvailableModel') }]);
const model = ref('');
const effort = ref((currentPreferences.value['reasoning_effort'] as string) ?? 'medium');
const stats = ref(currentPreferences.value['show_stats'] === true);

// Only offered where the group allows it. Hiding it is not the enforcement —
// the chat checks the same permission before drawing the line — it is so
// nobody is shown a switch that would do nothing.
const statsAllowed = computed(() => currentUser.value?.allow_stats !== false);

onMounted(() => {
  void api.get<{ models: Array<{ id: string; display_name: string; usable?: boolean }> }>('/api/models')
    .then(({ models: list }) => {
      const usable = list.filter((entry) => entry.usable !== false);
      choices.value = [
        { value: '', label: t('firstAvailableModel') },
        ...usable.map((entry) => ({ value: entry.id, label: entry.display_name })),
      ];
      // Only if it is still on the list: a model that has since been withdrawn
      // would otherwise leave the control naming nothing.
      const stored = currentPreferences.value['default_model_id'];
      if (typeof stored === 'string' && usable.some((entry) => entry.id === stored)) {
        model.value = stored;
      }
    })
    .catch(() => {
      choices.value = [{ value: '', label: t('couldNotLoadModels') }];
    });
});
</script>

<template>
  <div class="oa-settings-panel">
    <h2 class="oa-admin-section-title">{{ t('secChatDefaults') }}</h2>
    <p class="oa-field-hint">{{ t('chatDefaultsHint') }}</p>

    <OaField :label="t('defaultModel')">
      <OaSelect
        v-model="model"
        :choices="choices"
        @update:model-value="syncPreferences({ default_model_id: $event })"
      />
    </OaField>

    <OaSelectField
      v-model="effort"
      :label="t('defaultEffort')"
      :hint="t('defaultEffortHint')"
      :options="[
        { value: 'low', label: t('effortLow') },
        { value: 'medium', label: t('effortMedium') },
        { value: 'high', label: t('effortHigh') },
      ]"
      @update:model-value="syncPreferences({ reasoning_effort: $event })"
    />

    <OaSwitchField
      v-if="statsAllowed"
      v-model="stats"
      :label="t('showStats')"
      :hint="t('showStatsHint')"
      @update:model-value="syncPreferences({ show_stats: $event })"
    />
  </div>
</template>
