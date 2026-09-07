<script setup lang="ts">
// The address itself.
//
// Signed in it is the chat; signed out it is whatever the operator has put at
// the front door. The sign-in case never reaches here — the router's guard
// sends it to /login — so this is only ever the chat or the operator's page,
// and the URL stays `/` either way.

import { currentUser } from '@/stores/session';
import ChatLayout from '@/layouts/ChatLayout.vue';
import LandingView from './LandingView.vue';
</script>

<template>
  <ChatLayout v-if="currentUser" />
  <LandingView v-else />
</template>
