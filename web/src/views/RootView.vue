<script setup lang="ts">
// The address itself.
//
// Signed in it is the chat; signed out it is whatever the operator has put at
// the front door. The sign-in case never reaches here — the router's guard
// sends it to /login — so this is only ever the chat, the operator's own
// page, or the product's front page, and the URL stays `/` in every case.
//
// The front page is fetched when somebody actually lands on one, rather than
// riding on the first paint of everyone who came to chat. It is the same
// trade the terminal makes: every instance may show it, few do — the default
// front door is the sign-in card — and a signed-in account never sees it at
// all. Its stylesheet is not split, because this project ships one.

import { defineAsyncComponent } from 'vue';
import { currentUser, siteInfo } from '@/stores/session';
import ChatLayout from '@/layouts/ChatLayout.vue';
import LandingView from './LandingView.vue';

const FrontPage = defineAsyncComponent(() => import('./FrontPage.vue'));
</script>

<template>
  <ChatLayout v-if="currentUser" />
  <FrontPage v-else-if="siteInfo.landing?.mode === 'site'" />
  <LandingView v-else />
</template>
