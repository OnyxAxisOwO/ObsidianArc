<script setup lang="ts">
// The picture behind the interface, and how far the interface is seen through.
//
// Sliders, and live. Nobody knows what "35" looks like on any of these, so the
// control that works is the one you push until the screen is right — which
// means the screen has to change while you push it. They used to be number
// boxes behind an Apply button, and a number typed into a box that does
// nothing until a button somewhere else is pressed reads, correctly, as
// broken.

import { computed, ref } from 'vue';
import { ApiError, api } from '@/api/client';
import { ImageError, prepareImage } from '@/chat/image';
import OaRangeField from '@/components/OaRangeField.vue';
import { t } from '@/composables/useI18n';
import { useTheme } from '@/composables/useTheme';
import { setWallpaper, type Wallpaper } from '@/theme/theme';
import { syncPreferences } from '@/stores/session';

const theme = useTheme();
const current = computed(() => theme.paper());

// Empty until it has something to report. "A wallpaper is set" is a sentence
// about a picture the reader can already see behind the panel; this line is
// here for the upload, and for when one fails.
const status = ref('');
const picker = ref<HTMLInputElement | null>(null);

const dim = ref(current.value?.dim ?? 30);
const blur = ref(current.value?.blur ?? 0);
const translucency = ref(current.value?.translucency ?? 0);
const panelBlur = ref(current.value?.panelBlur ?? 0);

function readSliders(): Wallpaper | null {
  const existing = theme.paper();
  if (!existing) return null;
  return {
    url: existing.url,
    dim: dim.value,
    blur: blur.value,
    translucency: translucency.value,
    panelBlur: panelBlur.value,
  };
}

/** On every move: local only, so the change is on screen at once. */
function preview(): void {
  const next = readSliders();
  if (next) setWallpaper(next);
}

/** On release: the same value, this time remembered by the account. */
function persist(): void {
  const next = readSliders();
  if (next) syncPreferences({ wallpaper: next });
}

async function upload(file: File): Promise<void> {
  status.value = t('preparing');
  try {
    // The same downscale the composer uses. A phone photo as a wallpaper is
    // several megabytes of picture nobody will ever look at closely.
    const prepared = await prepareImage(file);
    URL.revokeObjectURL(prepared.previewURL);

    status.value = t('uploading');
    const { url } = await api.put<{ url: string }>('/api/preferences/wallpaper', {
      mime: prepared.mime,
      data: prepared.data,
    });

    const next: Wallpaper = {
      url,
      dim: dim.value,
      blur: blur.value,
      translucency: translucency.value,
      panelBlur: panelBlur.value,
    };
    setWallpaper(next);
    syncPreferences({ wallpaper: next });
    status.value = '';
  } catch (error) {
    status.value = error instanceof ImageError || error instanceof ApiError
      ? error.message
      : String(error);
  }
}

function onPick(): void {
  const node = picker.value;
  const file = node?.files?.[0];
  if (node) node.value = '';
  if (file) void upload(file);
}

async function clear(): Promise<void> {
  try {
    await api.delete('/api/preferences/wallpaper');
  } catch {
    // Even if the server call fails, taking it off screen is what was asked.
  }
  setWallpaper(null);
  syncPreferences({ wallpaper: null });
  status.value = '';
}
</script>

<template>
  <div class="oa-settings-panel">
    <h2 class="oa-admin-section-title">{{ t('secWallpaper') }}</h2>
    <p class="oa-field-hint">{{ status }}</p>

    <div class="oa-button-row">
      <button type="button" class="oa-btn" @click="picker?.click()">{{ t('chooseImage') }}</button>
      <button v-if="current" type="button" class="oa-btn oa-btn-danger" @click="clear">
        {{ t('remove') }}
      </button>
    </div>

    <OaRangeField
      v-model="dim"
      :label="t('dim')"
      :min="0"
      :max="100"
      :hint="t('dimHint')"
      @update:model-value="preview"
      @commit="persist"
    />
    <OaRangeField
      v-model="blur"
      :label="t('blur')"
      :min="0"
      :max="40"
      :format="(value) => `${value}px`"
      @update:model-value="preview"
      @commit="persist"
    />
    <!-- About the interface rather than the picture, but they belong here:
         they do nothing without a wallpaper, and they are how one becomes
         visible through the panels instead of only around them. -->
    <OaRangeField
      v-model="translucency"
      :label="t('panelTranslucency')"
      :min="0"
      :max="90"
      :hint="t('panelTranslucencyHint')"
      @update:model-value="preview"
      @commit="persist"
    />
    <OaRangeField
      v-model="panelBlur"
      :label="t('panelBlur')"
      :min="0"
      :max="40"
      :format="(value) => `${value}px`"
      :hint="t('panelBlurHint')"
      @update:model-value="preview"
      @commit="persist"
    />

    <input
      ref="picker"
      type="file"
      accept="image/png,image/jpeg,image/webp,image/avif"
      hidden
      @change="onPick"
    >
  </div>
</template>
