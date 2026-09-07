<script setup lang="ts">
// What somebody with no account sees at the address.
//
// Three shapes, chosen by the operator: the sign-in card (what the instance
// always did, and handled by the router's guard before this ever renders), a
// page they wrote, or the chat itself — optionally live, so a visitor can ask
// a couple of questions before deciding whether to sign up.
//
// The trial is the only part of this project that spends the operator's
// provider credit for somebody who has not identified themselves, so it holds
// nothing back on the client. The turn count is the server's, signed, and
// simply carried back and forth by this page; the per-address and
// instance-wide ceilings behind it are what hold when a client throws the
// token away. Losing this file entirely would cost the operator nothing.

import { computed, nextTick, ref } from 'vue';
import { useRouter } from 'vue-router';
import { ApiError } from '@/api/client';
import { streamTrial } from '@/api/trial';
import HomeNotice from '@/announce/HomeNotice.vue';
import OaIconButton from '@/components/OaIconButton.vue';
import OaMarkdown from '@/components/OaMarkdown.vue';
import OaThemeToggle from '@/components/OaThemeToggle.vue';
import { t } from '@/composables/useI18n';
import { IconSend, IconSpark } from '@/icons';
import { siteInfo } from '@/stores/session';
import SafeIntro from './SafeIntro.vue';

interface Turn {
  role: 'user' | 'assistant';
  content: string;
  failed?: boolean;
}

const router = useRouter();
const site = computed(() => siteInfo.value);
const landing = computed(() =>
  site.value.landing ?? { mode: 'login' as const, intro: '', trial: false, trial_turns: 0 });

const thread = ref<HTMLElement | null>(null);
const turns = ref<Turn[]>([]);
const question = ref('');
const busy = ref(false);
// The server's signed count of turns used. Opaque, and the only thing that
// makes the limit mean anything: the exchange itself is written here, so a
// page that simply forgot the earlier turns would otherwise look like a
// first-time visitor forever.
const continuation = ref('');
// What the server said is left after the last answer. Before the first, the
// configured allowance is the honest guess.
const left = ref(landing.value.trial_turns);

const finished = computed(() => left.value === 0);
const notice = computed(() => {
  if (!landing.value.trial) return t('setupBodyUser');
  if (finished.value) return t('trialFinished');
  return left.value === 1 ? t('trialLastTurn') : t('trialTurnsLeft', { count: left.value });
});

function toEnd(): void {
  void nextTick(() => {
    const node = thread.value;
    if (node) node.scrollTop = node.scrollHeight;
  });
}

async function ask(): Promise<void> {
  const text = question.value.trim();
  if (!text || busy.value) return;

  question.value = '';
  turns.value = [...turns.value, { role: 'user', content: text }, { role: 'assistant', content: '' }];
  const index = turns.value.length - 1;
  busy.value = true;
  toEnd();

  // Everything but the empty assistant turn just pushed to render into. The
  // question itself is already the last entry.
  const sending = turns.value.slice(0, -1).map((entry) => ({ role: entry.role, content: entry.content }));

  try {
    const result = await streamTrial(sending, continuation.value, (delta) => {
      const answer = turns.value[index];
      if (!answer) return;
      turns.value = turns.value.map((entry, at) =>
        (at === index ? { ...entry, content: entry.content + delta } : entry));
      toEnd();
    });
    continuation.value = result.continuation;
    left.value = result.turnsLeft;
  } catch (error) {
    turns.value = turns.value.map((entry, at) => (at === index
      ? { role: 'assistant', content: error instanceof ApiError ? error.message : t('trialFailed'), failed: true }
      : entry));
  } finally {
    busy.value = false;
  }
}
</script>

<template>
  <div class="oa-landing">
    <div class="oa-landing-head">
      <a class="oa-landing-brand" href="/" @click.prevent>
        <span class="oa-auth-mark"><IconSpark :size="15" /></span>
        <span>{{ site.name }}</span>
      </a>
      <span class="oa-header-spacer" />
      <!-- The same toggle the sign-in card carries, for the same reason: this
           may be the first thing anybody sees. -->
      <OaThemeToggle />
      <button type="button" class="oa-btn" @click="router.push('/login')">{{ t('landingSignIn') }}</button>
      <button
        v-if="site.registration_enabled"
        type="button"
        class="oa-btn primary"
        @click="router.push('/register')"
      >{{ t('landingRegister') }}</button>
    </div>

    <!-- The same standing notice the chat carries. "Above the home page" has
         to mean the page a visitor actually lands on, and for an instance with
         a public front door that is this one rather than the chat behind it. -->
    <HomeNotice />

    <div class="oa-landing-body">
      <div v-if="landing.mode === 'intro'" class="oa-landing-intro">
        <SafeIntro v-if="landing.intro.trim()" :html="landing.intro" />
        <!-- Nothing written yet: say what the instance is, from the setting
             the sign-in card already uses, rather than showing a blank page. -->
        <template v-else>
          <h1 class="oa-landing-title">{{ t('welcomeBack') }}</h1>
          <p v-if="site.description" class="oa-landing-sub">{{ site.description }}</p>
        </template>
      </div>

      <div v-else class="oa-landing-chat">
        <h1 class="oa-landing-title">{{ site.name }}</h1>
        <p v-if="site.description" class="oa-landing-sub">{{ site.description }}</p>

        <!-- Only where there is an exchange to hold. The thread is `flex: 1`,
             so rendering it empty on an instance with the trial switched off
             absorbs the whole column and pushes the notice and the way in to
             the bottom of the window. -->
        <div v-if="landing.trial" ref="thread" class="oa-landing-thread">
          <div
            v-for="(turn, index) in turns"
            :key="index"
            class="oa-landing-turn"
            :class="turn.role"
          >
            <div v-if="turn.role === 'user'" class="ai-user-bubble">{{ turn.content }}</div>
            <div v-else-if="turn.failed" class="ai-answer oa-landing-error">{{ turn.content }}</div>
            <OaMarkdown v-else class="ai-answer" :text="turn.content" />
          </div>
        </div>

        <!-- The shop window: the interface is visible, nothing can be sent.
             Saying so beats a composer that silently refuses. -->
        <p class="oa-landing-notice">{{ notice }}</p>

        <form
          v-if="landing.trial && !finished"
          class="oa-landing-composer"
          @submit.prevent="ask"
        >
          <input v-model="question" type="text" :placeholder="t('trialPlaceholder')" maxlength="4000">
          <OaIconButton class="ai-chat-send" type="submit" :label="t('send')" :disabled="busy">
            <IconSend :size="16" />
          </OaIconButton>
        </form>

        <div v-else class="oa-landing-finished">
          <div class="oa-landing-entry">
            <button
              v-if="site.registration_enabled"
              type="button"
              class="oa-btn primary"
              @click="router.push('/register')"
            >{{ t('trialSignUp') }}</button>
            <button type="button" class="oa-btn" @click="router.push('/login')">
              {{ t('trialSignIn') }}
            </button>
          </div>
        </div>
      </div>
    </div>
  </div>
</template>
