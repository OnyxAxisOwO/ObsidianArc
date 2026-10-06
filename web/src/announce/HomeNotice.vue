<script setup lang="ts">
// The instance's standing notice: a thin strip at the top of the page, and
// what opens from it.
//
// Not an announcement, despite the name they share. An announcement is dated,
// has a read state per person, and is done once it has been read. This is a
// property of the instance — a maintenance window, a house rule, a link
// everyone needs — and it stays up until an operator takes it down.
//
// That difference is why it lives in settings rather than in the
// announcements table: there is nothing to record about who has seen it, and
// the front door needs it before anyone has signed in. It is on the sign-in
// page, the sign-up page, the front page and the chat, so it says the same
// thing to whoever arrives wherever they arrive.
//
// The strip is one line. If the operator wrote a text behind it, the strip is
// a button, a line too long for it scrolls, and the text opens over the page,
// drawn by the renderer the announcements and the transcript use; if not, the
// line is the whole notice and is shown in full rather than cut short with
// nothing to open.

import { computed, ref } from 'vue';
import OaIconButton from '@/components/OaIconButton.vue';
import OaMarkdown from '@/components/OaMarkdown.vue';
import OaMarquee from '@/components/OaMarquee.vue';
import OaOverlay from '@/components/OaOverlay.vue';
import { t } from '@/composables/useI18n';
import { IconChevron, IconClose, IconInfo } from '@/icons';
import { siteInfo } from '@/stores/session';

defineProps<{
  /** Fixed to the top of the window, for a page that is one centred card. */
  floating?: boolean;
}>();

const DISMISSED_KEY = 'obsidian-arc-home-notice-dismissed';

const text = computed(() => siteInfo.value.home_notice?.text?.trim() ?? '');
const body = computed(() => siteInfo.value.home_notice?.body?.trim() ?? '');
const tone = computed(() => (siteInfo.value.home_notice?.tone === 'warning' ? 'warning' : 'info'));
const dismissible = computed(() => siteInfo.value.home_notice?.dismissible ?? true);

/**
 * A small non-cryptographic digest of the wording.
 *
 * Dismissal is remembered against the wording, not against a flag: an
 * operator who edits the notice is saying something new, and somebody who put
 * the old one away has not read it. The hash only has to change when the text
 * does and be short enough to sit in localStorage — FNV-1a is both, and costs
 * nothing next to pulling in a real one.
 */
function fingerprint(value: string): string {
  let hash = 0x811c9dc5;
  for (let i = 0; i < value.length; i++) {
    hash ^= value.charCodeAt(i);
    hash = Math.imul(hash, 0x01000193);
  }
  return (hash >>> 0).toString(36);
}

/** The line and the text behind it: changing either is saying something new. */
const signature = computed(() => fingerprint(`${text.value}\n${body.value}`));

function readDismissed(): string | null {
  try {
    return localStorage.getItem(DISMISSED_KEY);
  } catch {
    // Storage refused: the notice simply stays up, which is the safer way for
    // this particular thing to fail.
    return null;
  }
}

const dismissed = ref(readDismissed());
const reading = ref(false);

const visible = computed(() => {
  if (!text.value) return false;
  if (!dismissible.value) return true;
  return dismissed.value !== signature.value;
});

function dismiss(): void {
  const mark = signature.value;
  try {
    localStorage.setItem(DISMISSED_KEY, mark);
  } catch {
    // It closes for this view either way; it will be back on the next load.
  }
  dismissed.value = mark;
}
</script>

<template>
  <div
    v-if="visible"
    class="oa-home-notice"
    :class="[`tone-${tone}`, { 'has-body': !!body, floating }]"
  >
    <span class="oa-home-notice-icon" aria-hidden="true"><IconInfo :size="15" /></span>
    <!-- The whole strip is the button, not a "read more" beside the words: the
         line is the thing somebody is looking at when they decide to open it. -->
    <button
      v-if="body"
      type="button"
      class="oa-home-notice-main"
      aria-haspopup="dialog"
      :aria-label="`${text} — ${t('homeNoticeRead')}`"
      @click="reading = true"
    >
      <!-- One line, so a line wider than a phone runs past rather than being
           cut off: the end of a notice is often the part that matters. -->
      <OaMarquee class="oa-home-notice-text" :text="text" />
      <IconChevron class="oa-home-notice-go" :size="14" />
    </button>
    <!-- Plain text with the line breaks preserved in CSS: the operator writes
         this in a textarea and nothing here parses markup. -->
    <div v-else class="oa-home-notice-main">
      <span class="oa-home-notice-text">{{ text }}</span>
    </div>
    <OaIconButton
      v-if="dismissible"
      class="oa-icon-btn oa-home-notice-close"
      :label="t('close')"
      @click="dismiss"
    >
      <IconClose :size="14" />
    </OaIconButton>

    <OaOverlay
      v-if="reading"
      v-slot="{ close }"
      overlay-class="oa-announce-overlay"
      @close="reading = false"
    >
      <div class="oa-announce">
        <h2 class="oa-announce-title">{{ text }}</h2>
        <!-- The same renderer the transcript uses: Markdown to nodes, with no
             innerHTML anywhere in the path. It is written by an administrator
             and read by people who have not signed in. -->
        <OaMarkdown class="ai-answer oa-announce-body" :text="body" />
        <div class="oa-announce-foot">
          <span class="oa-drawer-foot-spacer" />
          <button type="button" class="oa-btn primary" @click="close">{{ t('close') }}</button>
        </div>
      </div>
    </OaOverlay>
  </div>
</template>
