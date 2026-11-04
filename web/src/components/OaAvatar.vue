<script setup lang="ts">
import { computed, ref, watch } from 'vue';
import type { Account } from '@/api/auth';
import { avatarSources, displayName, initials, safeAvatar } from '@/lib/account';

const props = defineProps<{
  account: Account;
  large?: boolean;
}>();

const attempt = ref(0);
const sources = computed(() => avatarSources(safeAvatar(props.account.avatar) ?? props.account.default_avatar ?? ''));
const source = computed(() => sources.value[attempt.value]);
watch(sources, () => { attempt.value = 0; });
</script>

<template>
  <span class="oa-avatar" :class="{ 'oa-avatar-lg': props.large }">
    <img v-if="source" :key="source" :src="source" alt="" :draggable="false" referrerpolicy="no-referrer" @error="attempt += 1">
    <template v-else>{{ initials(displayName(props.account)) }}</template>
  </span>
</template>
