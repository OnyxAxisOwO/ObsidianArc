<script setup lang="ts">
// Fullscreen lightbox overlay for viewing enlarged generated images.
//
// Teleports through OaOverlay to <body> so no ancestor container or transform
// clips or obscures it. Pressing Escape or clicking the dark backdrop closes it.

import { computed } from 'vue';
import { t } from '@/composables/useI18n';
import { IconClose, IconDownload } from '@/icons';
import OaOverlay from './OaOverlay.vue';

const props = defineProps<{
  src: string;
  alt?: string | undefined;
  downloadName?: string | undefined;
}>();

const emit = defineEmits<{ (event: 'close'): void }>();

// The same string goes into the picture and into the download link, and a
// javascript: link runs in this origin when it is clicked. Only the forms an
// image is served under get through, and anything else draws nothing.
//
// The URL parser drops tabs, newlines and other control characters before it
// reads a scheme, so "java<tab>script:" is a javascript: URL. The scheme is
// therefore read from the same string the parser will see.
function safeSource(value: string): string {
  // JS trim() also strips U+00A0, U+FEFF, U+3000 and U+2028, which the URL
  // parser leaves alone. Probing the untrimmed string let one of those in front
  // of "javascript:" hide the scheme, so the probe and the value are one string.
  const source = value.trim();
  const probe = source.replace(/[\u0000-\u0020\u007f]/g, '');
  const scheme = /^([a-z][a-z0-9+.-]*):/i.exec(probe)?.[1]?.toLowerCase();
  if (!scheme) return source;
  if (scheme === 'http' || scheme === 'https' || scheme === 'blob') return source;
  if (scheme === 'data' && /^data:image\/(?:png|jpeg|webp|gif|avif);base64,[a-z0-9+/]+=*$/i.test(probe)) {
    return source;
  }
  return '';
}

const source = computed(() => safeSource(props.src));
</script>

<template>
  <OaOverlay v-slot="{ close }" overlay-class="oa-lightbox-overlay" :label="props.alt || t('zoomImage')" @close="emit('close')">
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
      <img v-if="source" :src="source" class="oa-lightbox-img" :alt="props.alt || ''">
      <p v-if="props.alt" class="oa-lightbox-caption">
        {{ props.alt }}
      </p>
      <div v-if="source" class="oa-lightbox-actions">
        <a
          :href="source"
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
