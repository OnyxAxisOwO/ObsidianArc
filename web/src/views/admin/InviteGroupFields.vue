<script setup lang="ts">
// The group an invite puts its account in, and for how many days. The partner
// and batch forms both ask it, in different places among their other fields,
// so it is one block each form places where it already stood.

import { computed } from 'vue';
import type { Group } from '@/admin/api';
import OaNumberField from '@/components/OaNumberField.vue';
import OaSelectField from '@/components/OaSelectField.vue';
import { t } from '@/composables/useI18n';

const props = defineProps<{
  groups: Pick<Group, 'id' | 'name'>[];
  error: string;
}>();

const groupId = defineModel<string>('groupId', { required: true });
const daysFrom = defineModel<number | null>('daysFrom', { required: true });
const daysTo = defineModel<number | null>('daysTo', { required: true });

// invite.MaxGroupDays (internal/invite/invite.go). A larger number is refused
// on save, so the field stops at the same figure rather than letting the
// operator find out after typing it.
const MAX_GROUP_DAYS = 3650;

const options = computed(() => [
  { value: '', label: t('inviteGroupNone') },
  ...props.groups.map((group) => ({ value: group.id, label: group.name })),
]);
</script>

<template>
  <p v-if="props.error" class="oa-field-hint" role="alert">{{ props.error }}</p>
  <OaSelectField v-model="groupId" :label="t('inviteGroup')" :options="options" />
  <template v-if="groupId">
    <OaNumberField v-model="daysFrom" :label="t('inviteGroupDaysFrom')" :min="0" :max="MAX_GROUP_DAYS" :hint="t('inviteGroupDaysHint')" />
    <OaNumberField v-model="daysTo" :label="t('inviteGroupDaysTo')" :min="0" :max="MAX_GROUP_DAYS" :placeholder="t('inviteGroupDaysFixed')" />
  </template>
</template>
