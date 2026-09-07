<script setup lang="ts">
// The instance's standing notice, above the chat.
//
// Not an announcement, despite the name they share. An announcement is dated,
// has a read state per person, and is done once it has been read. This is a
// property of the instance — a maintenance window, a house rule, a link
// everyone needs — and it stays up until an operator takes it down.
//
// That difference is why it lives in settings rather than in the
// announcements table: there is nothing to record about who has seen it, and
// the front door needs it before anyone has signed in.

import { computed, ref } from 'vue';
import OaIconButton from '@/components/OaIconButton.vue';
import { t } from '@/composables/useI18n';
import { IconClose } from '@/icons';
import { siteInfo } from '@/stores/session';

const DISMISSED_KEY = 'obsidian-arc-home-notice-dismissed';

const text = computed(() => siteInfo.value.home_notice?.text?.trim() ?? '');
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

const visible = computed(() => {
  if (!text.value) return false;
  if (!dismissible.value) return true;
  return dismissed.value !== fingerprint(text.value);
});

function dismiss(): void {
  const signature = fingerprint(text.value);
  try {
    localStorage.setItem(DISMISSED_KEY, signature);
  } catch {
    // It closes for this view either way; it will be back on the next load.
  }
  dismissed.value = signature;
}
</script>

<template>
  <div v-if="visible" class="oa-home-notice">
    <!-- Plain text with the line breaks preserved in CSS: the operator writes
         this in a textarea and nothing here parses markup. -->
    <div class="oa-home-notice-text">{{ text }}</div>
    <OaIconButton
      v-if="dismissible"
      class="oa-icon-btn oa-home-notice-close"
      :label="t('close')"
      @click="dismiss"
    >
      <IconClose :size="15" />
    </OaIconButton>
  </div>
</template>
