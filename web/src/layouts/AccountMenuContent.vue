<script setup lang="ts">
import { computed, ref } from 'vue';
import { useRouter } from 'vue-router';
import { logout, type Account } from '@/api/auth';
import { ApiError } from '@/api/client';
import OaAvatar from '@/components/OaAvatar.vue';
import OaMenuItem from '@/components/OaMenuItem.vue';
import OaScrollArea from '@/components/OaScrollArea.vue';
import { t } from '@/composables/useI18n';
import { usePanelExit } from '@/composables/usePanelExit';
import { IconArchive, IconChart, IconGear, IconImage, IconInfo, IconKey, IconLogout, IconMessage, IconPulse, IconSliders, IconTerminal, IconTrophy } from '@/icons';
import { displayName } from '@/lib/account';
import { userPanels } from '@/plugins/registry';
import { feedbackUnread } from '@/stores/feedback';
import { forget, isAdmin } from '@/stores/session';
import { panelAllowed } from '@/views/panels';
import '@/styles/account-menu.scss';

const props = defineProps<{ account: Account; close: () => void }>();

const router = useRouter();
const panels = usePanelExit();

// Most panels go together after Feedback; one about what the account may
// spend asks to sit under Usage instead (UserPanelSpec.menu).
const usagePanels = computed(() => userPanels().filter((panel) => panel.menu === 'usage'));
const otherPanels = computed(() => userPanels().filter((panel) => panel.menu !== 'usage'));

// The community section is the only one that can be empty: both of its
// entries are things an operator publishes. A label over nothing reads as a
// list that failed to load, so the section goes with its last entry.
const communityShown = computed(() => panelAllowed('uptime') || panelAllowed('leaderboard'));

// What the head says about the account besides its name. Text, not chips: a
// group name in an uppercase pill was the loudest thing in the menu, and it
// is a fact about the account, not a control or a warning.
const facts = computed(() => {
  const out: string[] = [];
  if (props.account.group_name) out.push(props.account.group_name);
  if (isAdmin.value) out.push(t(props.account.role === 'super_admin' ? 'superAdmin' : 'admin'));
  return out;
});

function go(close: () => void, path: string): void {
  close();
  void router.push(path);
}

// Panels open beside the backoffice when it is what is on screen, rather than
// replacing it with the chat (see usePanelExit). The menu closes first either
// way, as it always has.
function openPanel(close: () => void, path: string): void {
  close();
  panels.open(path);
}

// The account stays on screen until the server has ended the session. Only a
// 401 proves it is already gone; any other failure leaves the session live, and
// the next load would sign this browser straight back in, so the menu says so
// instead of showing a signed-out page that does not hold.
const SIGN_OUT_WAIT_MS = 10_000;
const signOutError = ref('');
const signingOut = ref(false);

async function signOut(close: () => void): Promise<void> {
  if (signingOut.value) return;
  signingOut.value = true;
  signOutError.value = '';
  // A stalled request must not keep the menu on the account indefinitely.
  const controller = new AbortController();
  const deadline = window.setTimeout(() => controller.abort(), SIGN_OUT_WAIT_MS);
  try {
    await logout(controller.signal);
  } catch (failure) {
    if (!(failure instanceof ApiError && failure.isAuth)) {
      signOutError.value = t('signOutFailed');
      return;
    }
  } finally {
    window.clearTimeout(deadline);
    signingOut.value = false;
  }
  close();
  forget();
  await router.replace('/login');
}
</script>

<template>
      <div class="oa-acct-head">
        <OaAvatar :account="props.account" />
        <div class="oa-acct-id">
          <span class="oa-acct-name">{{ displayName(props.account) }}</span>
          <span class="oa-acct-handle">@{{ props.account.username }}</span>
          <!-- A line of text, so a keyboard, a touch screen and a screen
               reader have it too; the title is only for the end an ellipsis
               hid. It takes the width the name and handle leave rather than
               setting it (contain in the stylesheet), since a long address
               used to decide how wide the whole menu came out. -->
          <span
            v-if="props.account.email"
            class="oa-acct-email"
            :title="props.account.email"
          >{{ props.account.email }}</span>
        </div>
        <div v-if="facts.length" class="oa-acct-facts">
          <span v-for="fact in facts" :key="fact" class="oa-acct-fact">{{ fact }}</span>
        </div>
      </div>

      <!-- Only the sections scroll. The head says whose menu this is and the
           foot is the way out, so on a short window both stay put and the
           list between them gives way, on an overlay track that takes no
           width from the rows. -->
      <OaScrollArea wrap-class="oa-acct-scroll-wrap" scroll-class="oa-acct-scroll">
        <div class="oa-acct-section" role="group" :aria-label="t('account')">
          <span class="oa-acct-label" aria-hidden="true">{{ t('account') }}</span>
          <OaMenuItem :title="t('settings')" @click="openPanel(close, '/settings')">
            <template #leading><IconGear :size="14" /></template>
          </OaMenuItem>
          <OaMenuItem :title="t('navUsage')" @click="openPanel(close, '/usage')">
            <template #leading><IconChart :size="14" /></template>
          </OaMenuItem>
          <OaMenuItem
            v-for="panel in usagePanels"
            :key="panel.slug"
            :title="panel.title()"
            @click="openPanel(close, `/x/${panel.slug}`)"
          >
            <template #leading><component :is="panel.icon ?? IconMessage" :size="14" /></template>
          </OaMenuItem>
          <OaMenuItem :title="t('archivedConversations')" @click="openPanel(close, '/archive')">
            <template #leading><IconArchive :size="14" /></template>
          </OaMenuItem>
        </div>

        <!-- The keys and the terminal stay side by side: they are the two
             things here for somebody who works from a keyboard, and the image
             lab is the other place something gets made rather than read. -->
        <div class="oa-acct-section" role="group" :aria-label="t('acctMenuTools')">
          <span class="oa-acct-label" aria-hidden="true">{{ t('acctMenuTools') }}</span>
          <OaMenuItem :title="t('imageLab')" @click="openPanel(close, '/image-lab')">
            <template #leading><IconImage :size="14" /></template>
          </OaMenuItem>
          <OaMenuItem :title="t('apiKeys')" @click="openPanel(close, '/keys')">
            <template #leading><IconKey :size="14" /></template>
          </OaMenuItem>
          <!-- Hidden only when the group has it switched off; a server too
               old to say is not a refusal. -->
          <OaMenuItem
            v-if="props.account.allow_terminal !== false"
            :title="t('navTerminal')"
            @click="go(close, '/terminal')"
          >
            <template #leading><IconTerminal :size="14" /></template>
          </OaMenuItem>
        </div>

        <div v-if="communityShown" class="oa-acct-section" role="group" :aria-label="t('acctMenuCommunity')">
          <span class="oa-acct-label" aria-hidden="true">{{ t('acctMenuCommunity') }}</span>
          <OaMenuItem
            v-if="panelAllowed('uptime')"
            :title="t('uptimeTitle')"
            @click="openPanel(close, '/uptime')"
          >
            <template #leading><IconPulse :size="14" /></template>
          </OaMenuItem>
          <OaMenuItem
            v-if="panelAllowed('leaderboard')"
            :title="t('leaderboardTitle')"
            @click="openPanel(close, '/leaderboard')"
          >
            <template #leading><IconTrophy :size="14" /></template>
          </OaMenuItem>
        </div>

        <div class="oa-acct-section" role="group" :aria-label="t('acctMenuSupport')">
          <span class="oa-acct-label" aria-hidden="true">{{ t('acctMenuSupport') }}</span>
          <OaMenuItem :title="t('feedback')" @click="openPanel(close, '/feedback')">
            <template #leading><IconMessage :size="14" /></template>
            <template v-if="feedbackUnread" #trailing>
              <span class="oa-menu-unread" :title="t('feedbackHasReply')" />
            </template>
          </OaMenuItem>
          <OaMenuItem
            v-for="panel in otherPanels"
            :key="panel.slug"
            :title="panel.title()"
            @click="openPanel(close, `/x/${panel.slug}`)"
          >
            <template #leading><component :is="panel.icon ?? IconMessage" :size="14" /></template>
          </OaMenuItem>
          <OaMenuItem :title="t('about')" @click="openPanel(close, '/about')">
            <template #leading><IconInfo :size="14" /></template>
          </OaMenuItem>
        </div>
      </OaScrollArea>

      <div class="oa-acct-foot">
        <OaMenuItem
          v-if="isAdmin"
          :title="t('administration')"
          @click="go(close, '/admin')"
        >
          <template #leading><IconSliders :size="14" /></template>
        </OaMenuItem>
        <OaMenuItem :title="t('signOut')" @click="signOut(close)">
          <template #leading><IconLogout :size="14" /></template>
        </OaMenuItem>
        <p v-if="signOutError" class="oa-menu-empty" role="alert">{{ signOutError }}</p>
      </div>

</template>
