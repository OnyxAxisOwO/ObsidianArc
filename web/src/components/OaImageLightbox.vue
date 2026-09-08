<script setup lang="ts">
// Fullscreen lightbox overlay for viewing enlarged generated images.
//
// Teleports through OaOverlay to <body> so no ancestor container or transform
// clips or obscures it. Pressing Escape or clicking the dark backdrop closes it.

import { t } from '@/composables/useI18n';
import { IconClose, IconDownload } from '@/icons';
import OaOverlay from './OaOverlay.vue';

const props = defineProps<{
  src: string;
  alt?: string | undefined;
  downloadName?: string | undefined;
}>();

const emit = defineEmits<{ (event: 'close'): void }>();
</script>

<template>
  <OaOverlay v-slot="{ close }" overlay-class="oa-lightbox-overlay" @close="emit('close')">
    <div class="oa-lightbox-card">
      <button
        type="button"
        class="oa-lightbox-close"
        :title="t('close')"
        :aria-label="t('close')"
        @click="close"
      >
        <IconClose :size="20" />
      </button>
      <img :src="props.src" class="oa-lightbox-img" :alt="props.alt || ''">
      <p v-if="props.alt" class="oa-lightbox-caption">
        {{ props.alt }}
      </p>
      <div class="oa-lightbox-actions">
        <a
          :href="props.src"
          :download="props.downloadName || 'image.png'"
          target="_blank"
          rel="noopener"
          class="oa-btn primary"
          style="text-decoration: none; display: inline-flex; align-items: center; gap: 6px;"
        >
          <IconDownload :size="14" />
          <span>{{ t('downloadImage') }}</span>
        </a>
      </div>
    </div>
  </OaOverlay>
</template>
