<script setup lang="ts">
import { computed } from 'vue';
import type { Account } from '@/api/auth';
import { displayName, initials, safeAvatar } from '@/lib/account';

const props = defineProps<{
  account: Account;
  large?: boolean;
}>();

const source = computed(() => safeAvatar(props.account.avatar));
</script>

<template>
  <span class="oa-avatar" :class="{ 'oa-avatar-lg': props.large }">
    <img v-if="source" :src="source" alt="" :draggable="false">
    <template v-else>{{ initials(displayName(props.account)) }}</template>
  </span>
</template>
