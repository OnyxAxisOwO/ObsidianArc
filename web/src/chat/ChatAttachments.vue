<script setup lang="ts">
// The row of thumbnails under a message, or above the composer.
//
// A discarded image has no URL to load. Fetching it would produce a broken
// thumbnail and a 404 in the console, which reads as a fault rather than as
// the retention policy working.

import { attachmentURL, type AttachmentRef } from '@/api/chat';
import { t } from '@/composables/useI18n';
import { IconImage } from '@/icons';

const props = defineProps<{ images: readonly AttachmentRef[] }>();
</script>

<template>
  <div class="ai-chat-attachments">
    <template v-for="image in props.images" :key="image.id">
      <div
        v-if="image.discarded"
        class="ai-chat-attachment ai-chat-attachment-gone"
        :title="t('imageDiscarded')"
        :aria-label="t('imageDiscarded')"
      >
        <IconImage :size="16" />
      </div>
      <div v-else class="ai-chat-attachment">
        <img class="ai-chat-attachment-img" :src="attachmentURL(image.id)" alt="" :draggable="false">
      </div>
    </template>
  </div>
</template>
