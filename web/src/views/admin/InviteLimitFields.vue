<script setup lang="ts">
// How often an invite can be used and for how long it is valid, asked the same
// way by the partner and the batch forms.

import OaNumberField from '@/components/OaNumberField.vue';
import { t } from '@/composables/useI18n';

const maxUses = defineModel<number | null>('maxUses', { required: true });
const expiresDays = defineModel<number | null>('expiresDays', { required: true });
</script>

<template>
  <OaNumberField v-model="maxUses" :label="t('inviteMaxUses')" :min="0" :max="100000" :hint="t('inviteMaxUsesHint')" />
  <OaNumberField
    v-model="expiresDays"
    :label="t('inviteExpiresDays')"
    :min="0"
    :placeholder="t('noLimit')"
    :hint="t('inviteExpiresDaysHint')"
  />
</template>
