<script setup lang="ts">
// The announcement itself, over the page.
//
// A sheet rather than a column, because it is the one thing here that is
// genuinely modal: it is shown unprompted and the reader has to deal with it
// before carrying on.

import { computed, onBeforeUnmount, ref } from 'vue';
import type { Announcement } from '@/api/announcements';
import OaMarkdown from '@/components/OaMarkdown.vue';
import OaOverlay from '@/components/OaOverlay.vue';
import { t } from '@/composables/useI18n';
import { relativeTime } from '@/lib/format';

const props = defineProps<{ announcement: Announcement }>();
const emit = defineEmits<{ (event: 'dismiss'): void }>();

/**
 * A delay is there to make somebody read the first line the first time.
 * Reopening one from the history is not that, so it is not made to wait
 * again. The countdown is on the button so the wait reads as finite rather
 * than broken; the server caps how long it can be.
 */
const remaining = ref(props.announcement.read
  ? 0
  : Math.max(0, Math.min(60, Math.round(props.announcement.dismiss_after_seconds))));

let ticker = 0;
if (remaining.value > 0) {
  ticker = window.setInterval(() => {
    remaining.value -= 1;
    if (remaining.value <= 0) {
      window.clearInterval(ticker);
      ticker = 0;
    }
  }, 1000);
}

onBeforeUnmount(() => {
  if (ticker) window.clearInterval(ticker);
});

const waiting = computed(() => remaining.value > 0);
const dismissLabel = computed(() => (waiting.value
  ? t('announcementDismissIn', { count: remaining.value })
  : t('announcementDismiss')));
</script>

<template>
  <OaOverlay
    v-slot="{ close }"
    overlay-class="oa-announce-overlay"
    :dismissible="!waiting"
    @close="emit('dismiss')"
  >
    <div class="oa-announce">
      <h2 class="oa-announce-title">{{ props.announcement.title }}</h2>
      <!-- The same renderer the transcript uses: Markdown to nodes, with no
           innerHTML anywhere in the path. An announcement is written by an
           administrator, but it is read by everyone, and "the author was
           trusted" is not a property worth building a second path around. -->
      <OaMarkdown class="ai-answer oa-announce-body" :text="props.announcement.body" />
      <div class="oa-announce-foot">
        <span class="oa-announce-when">{{ relativeTime(props.announcement.created_at) }}</span>
        <span class="oa-drawer-foot-spacer" />
        <button type="button" class="oa-btn primary" :disabled="waiting" @click="close">
          {{ dismissLabel }}
        </button>
      </div>
    </div>
  </OaOverlay>
</template>
