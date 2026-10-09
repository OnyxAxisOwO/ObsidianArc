<script setup lang="ts">
// The notice that a newer release is out, offered once per page load.
//
// A panel rather than a modal: it is the backoffice's own way of putting a
// thing to read beside the page, the same column the forms open in. It can
// be skipped for this version, which is remembered, or put away for this page
// load, which is not — the same dashboard line keeps saying so afterwards.

import { computed, ref } from 'vue';
import { offeredRelease, releaseLink, skippedVersion, skipVersion, updateStatus } from '@/admin/update';
import OaMarkdown from '@/components/OaMarkdown.vue';
import OaPanel from '@/components/OaPanel.vue';
import { currentLanguage, t } from '@/composables/useI18n';

const skipped = ref(skippedVersion());
const putAway = ref(false);

const release = computed(() => offeredRelease(updateStatus.value, skipped.value));
const link = computed(() => (release.value ? releaseLink(release.value) : null));
const published = computed(() => {
  const when = release.value?.published_at;
  if (!when) return '';
  const date = new Date(when);
  if (Number.isNaN(date.getTime())) return '';
  const locale = currentLanguage() === 'zh' ? 'zh-CN' : 'en-US';
  return date.toLocaleDateString(locale, { year: 'numeric', month: 'long', day: 'numeric' });
});

function skip(): void {
  const current = release.value;
  if (!current) return;
  skipVersion(current.latest);
  skipped.value = current.latest;
}

function later(): void {
  putAway.value = true;
}
</script>

<template>
  <OaPanel
    v-if="release && !putAway"
    :title="t('updateTitle', { version: release.latest })"
    :footer="false"
    :width="480"
    @close="later"
  >
    <p class="oa-update-meta">
      <span>{{ t('updateCurrentVersion', { version: release.current }) }}</span>
      <span v-if="published">{{ t('updatePublished', { date: published }) }}</span>
    </p>
    <div class="oa-update-notes">
      <OaMarkdown v-if="release.notes" :text="release.notes" />
      <p v-else class="oa-field-hint">{{ t('updateNotesEmpty') }}</p>
    </div>
    <div class="oa-update-actions">
      <a v-if="link" class="oa-btn primary" :href="link" target="_blank" rel="noopener noreferrer">{{ t('updateOpenRelease') }}</a>
      <button type="button" class="oa-btn" @click="skip">{{ t('updateSkipVersion') }}</button>
      <button type="button" class="oa-btn" @click="later">{{ t('updateLater') }}</button>
    </div>
  </OaPanel>
</template>
