<script setup lang="ts">
// A plugin's buttons in the account panel. Each asks in place, the way every
// other account-ending action there does — OaConfirmButton, never
// window.confirm().

import { ref } from 'vue';
import { ApiError } from '@/api/client';
import { maskUser } from '@/admin/safeMode';
import OaConfirmButton from '@/components/OaConfirmButton.vue';
import OaFormSection from '@/components/OaFormSection.vue';
import { t } from '@/composables/useI18n';
import type { UserActionSpec } from '@/plugins/types';

const props = defineProps<{
  spec: UserActionSpec;
  userId: string;
  username: string;
}>();

const emit = defineEmits<{
  (event: 'done', closePanel: boolean): void;
  (event: 'failed', message: string): void;
}>();

const busy = ref(false);
const flash = ref('');

async function run(button: UserActionSpec['buttons'][number]): Promise<void> {
  busy.value = true;
  flash.value = '';
  try {
    flash.value = await button.run(props.userId);
    emit('done', !!button.closesPanel);
  } catch (failure) {
    emit('failed', failure instanceof ApiError ? failure.message : String(failure));
  } finally {
    busy.value = false;
  }
}
</script>

<template>
  <OaFormSection :title="spec.title()" :hint="spec.hint?.()" />
  <div class="oa-2fa-admin-row">
    <OaConfirmButton
      v-for="(button, index) in spec.buttons"
      :key="index"
      class="oa-btn"
      :class="{ 'oa-btn-danger': button.danger }"
      :label="button.label()"
      :armed-label="t('confirmWord')"
      :armed-title="button.confirm(maskUser(username))"
      :resting-title="button.label()"
      :disabled="busy"
      @confirm="run(button)"
    />
  </div>
  <p v-if="flash" class="oa-field-hint" role="status">{{ flash }}</p>
</template>
