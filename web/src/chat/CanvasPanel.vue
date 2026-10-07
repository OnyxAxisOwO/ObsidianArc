<script setup lang="ts">
// The Canvas column: a model's page, running in a frame that cannot reach
// anything of this site.
//
// An OaPanel like every other column, so it narrows the conversation rather
// than covering it — the reader is usually looking at the page and the answer
// that explains it at once.
//
// The page is handed over by postMessage once the frame's shell says it is
// listening. Posting before then would land on the blank document the frame
// starts with and be lost; posting on the iframe's load event instead races
// the shell's own script on some engines. Waiting for the shell to speak first
// is the one order that cannot go wrong.

import { useEventListener } from '@vueuse/core';
import { ref } from 'vue';
import OaIconButton from '@/components/OaIconButton.vue';
import OaPanel from '@/components/OaPanel.vue';
import { t } from '@/composables/useI18n';
import { IconRefresh } from '@/icons';
import {
  CANVAS_PATH, CANVAS_SANDBOX, READY_MESSAGE, RUN_MESSAGE,
  canvasRun, canvasSource, closeCanvas, rerunCanvas,
} from './canvas';

const frame = ref<HTMLIFrameElement | null>(null);

useEventListener(window, 'message', (event: MessageEvent) => {
  // The sandboxed frame has an opaque origin, so its messages arrive with
  // origin "null" and the origin cannot identify it. Its window can: only the
  // frame this panel drew is answered, and only with the page it was opened for.
  const target = frame.value?.contentWindow;
  if (!target || event.source !== target) return;
  const data = event.data as { type?: unknown } | null;
  if (!data || data.type !== READY_MESSAGE || canvasSource.value === null) return;
  target.postMessage({ type: RUN_MESSAGE, html: canvasSource.value }, '*');
});

defineExpose({ frame });
</script>

<template>
  <OaPanel
    :title="t('canvasTitle')"
    :footer="false"
    :width="560"
    body-class="oa-canvas-body"
    @close="closeCanvas"
  >
    <template #actions>
      <OaIconButton class="oa-icon-btn" :label="t('canvasReload')" @click="rerunCanvas">
        <IconRefresh :size="15" />
      </OaIconButton>
    </template>
    <p class="oa-canvas-notice">{{ t('canvasNotice') }}</p>
    <!-- Keyed on the run counter: every run, including a second run of the
         same page, is a fresh document from the server rather than a second
         write into one a previous page has had its hands on. -->
    <iframe
      :key="canvasRun"
      ref="frame"
      class="oa-canvas-frame"
      :src="CANVAS_PATH"
      :sandbox="CANVAS_SANDBOX"
      :title="t('canvasTitle')"
      referrerpolicy="no-referrer"
    />
  </OaPanel>
</template>
