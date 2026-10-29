<script setup lang="ts">
// A model's mark: the operator's picture, a drawn mark, or its initials.
//
// One component for the picker and the backoffice's editor, so the preview an
// operator chooses from is the tile a reader will see, not a second drawing
// of it that can drift.

import { computed, onMounted } from 'vue';
import { initials, safeAvatar } from '@/lib/account';
import { MARK_NONE, MARK_PREFIX, guessMark, loadVendorMarks, markByID } from './model-marks';

const props = defineProps<{ avatar?: string; name: string }>();

onMounted(() => { void loadVendorMarks(); });

const resolved = computed(() => {
  const avatar = (props.avatar ?? '').trim();
  if (avatar === MARK_NONE) return { text: initials(props.name) };
  if (avatar.startsWith(MARK_PREFIX)) {
    const mark = markByID(avatar.slice(MARK_PREFIX.length));
    if (mark) return { mark };
  }
  // Only what the page's image policy lets through, the rule an account's
  // avatar is held to: anything else would be a broken picture.
  const src = avatar ? safeAvatar(avatar) : null;
  if (src) return { src };
  const guessed = avatar ? null : guessMark(props.name);
  return guessed ? { mark: guessed } : { text: initials(props.name) };
});
</script>

<template>
  <span class="ai-model-tile" :class="{ marked: !!resolved.mark }" aria-hidden="true">
    <img v-if="resolved.src" :src="resolved.src" alt="">
    <svg
      v-else-if="resolved.mark"
      viewBox="0 0 24 24"
      width="15"
      height="15"
      fill="none"
      stroke="currentColor"
      stroke-width="1.7"
      stroke-linecap="round"
      stroke-linejoin="round"
    >
      <path
        v-for="(shape, index) in resolved.mark.shapes"
        :key="index"
        :d="shape.d"
        :transform="shape.transform"
        :fill="shape.fill ? 'currentColor' : 'none'"
        :stroke="shape.fill ? 'none' : 'currentColor'"
        fill-rule="evenodd"
      />
    </svg>
    <template v-else>{{ resolved.text }}</template>
  </span>
</template>
