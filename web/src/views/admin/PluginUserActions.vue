<script setup lang="ts">
// A plugin's buttons in the account panel. Each asks in place, the way every
// other account-ending action there does — OaConfirmButton, never
// window.confirm().

import { ref } from 'vue';
import { ApiError } from '@/api/client';
import { maskUser } from '@/admin/safeMode';
import OaConfirmButton from '@/components/OaConfirmButton.vue';
import OaRow from '@/components/OaRow.vue';
import { t } from '@/composables/useI18n';
import AdminControlCard from './AdminControlCard.vue';
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
  <!-- A card like the user editor's own sections, so a plugin's actions read
       as one more of them rather than as something bolted on below. -->
  <AdminControlCard :title="spec.title()" :hint="spec.hint?.()">
    <OaRow>
      <OaConfirmButton
        v-for="(button, index) in spec.buttons"
        :key="index"
        class="oa-btn small"
        :class="{ 'oa-btn-danger': button.danger }"
        :label="button.label()"
        :armed-label="t('confirmWord')"
        :armed-title="button.confirm(maskUser(username))"
        :resting-title="button.label()"
        :disabled="busy"
        @confirm="run(button)"
      />
    </OaRow>
    <p v-if="flash" class="oa-group-flash ok" role="status">{{ flash }}</p>
  </AdminControlCard>
</template>
