<script setup lang="ts">
// How a model is marked in the picker: guessed from its name, one of the
// drawn marks, its initials, or a picture.
//
// The picture is stored inline. The page's image policy is `'self' data:
// blob:`, so a link to another site would be a broken tile for every reader,
// and the policy exists so a page view does not tell a third party who is
// reading. An upload is shrunk to a 64px tile here, which is what fits the
// avatar column (8 KiB) and all a 30px tile can show anyway.

import { computed, ref } from 'vue';
import OaTextField from '@/components/OaTextField.vue';
import { t } from '@/composables/useI18n';
import { IconImage } from '@/icons';
import ModelTile from '@/chat/ModelTile.vue';
import { MARK_NONE, MARK_PREFIX, guessMark, modelMarks } from '@/chat/model-marks';

const avatar = defineModel<string>({ required: true });
const props = defineProps<{ name: string }>();

/** The avatar column's limit, in characters. */
const MAX_CHARS = 8 * 1024;

const file = ref<HTMLInputElement | null>(null);
const error = ref('');

const isImage = computed(() => !!avatar.value && !avatar.value.startsWith(MARK_PREFIX));
const guessed = computed(() => guessMark(props.name || ''));
const link = computed({
  get: () => (isImage.value && !avatar.value.startsWith('data:') ? avatar.value : ''),
  set: (next: string) => { avatar.value = next.trim(); },
});

function choose(value: string): void {
  error.value = '';
  avatar.value = value;
}

/**
 * Drawn onto a square, cropped to the middle, at the largest size that still
 * fits the column. WebP where the browser can write it, PNG where it cannot.
 */
async function shrink(source: File): Promise<string | null> {
  const url = URL.createObjectURL(source);
  try {
    const image = new Image();
    image.src = url;
    await image.decode();
    for (const size of [64, 48, 32]) {
      const canvas = document.createElement('canvas');
      canvas.width = size;
      canvas.height = size;
      const ctx = canvas.getContext('2d');
      if (!ctx) return null;
      const side = Math.min(image.naturalWidth, image.naturalHeight) || size;
      ctx.drawImage(
        image,
        (image.naturalWidth - side) / 2, (image.naturalHeight - side) / 2, side, side,
        0, 0, size, size,
      );
      for (const type of ['image/webp', 'image/png']) {
        const data = canvas.toDataURL(type, 0.86);
        if (data.startsWith(`data:${type}`) && data.length <= MAX_CHARS) return data;
      }
    }
    return null;
  } finally {
    URL.revokeObjectURL(url);
  }
}

async function onFile(event: Event): Promise<void> {
  const input = event.target as HTMLInputElement;
  const picked = input.files?.[0];
  input.value = '';
  if (!picked) return;
  error.value = '';
  try {
    const data = await shrink(picked);
    if (data) avatar.value = data;
    else error.value = t('modelIconTooLarge');
  } catch {
    error.value = t('modelIconUnreadable');
  }
}
</script>

<template>
  <div class="oa-field oa-model-icon-field">
    <span class="oa-field-label">{{ t('modelIcon') }}</span>
    <div class="oa-model-icon-grid" role="radiogroup" :aria-label="t('modelIcon')">
      <button
        type="button"
        class="oa-model-icon-choice"
        role="radio"
        :aria-checked="!avatar"
        :class="{ active: !avatar }"
        @click="choose('')"
      >
        <ModelTile avatar="" :name="props.name || '?'" />
        <span>{{ guessed ? t('modelIconAutoNamed', { name: guessed.name ?? '' }) : t('modelIconAuto') }}</span>
      </button>
      <button
        v-for="mark in modelMarks"
        :key="mark.id"
        type="button"
        class="oa-model-icon-choice"
        role="radio"
        :aria-checked="avatar === MARK_PREFIX + mark.id"
        :class="{ active: avatar === MARK_PREFIX + mark.id }"
        @click="choose(MARK_PREFIX + mark.id)"
      >
        <ModelTile :avatar="MARK_PREFIX + mark.id" :name="props.name" />
        <span>{{ mark.name ?? (mark.labelKey ? t(mark.labelKey) : mark.id) }}</span>
      </button>
      <button
        type="button"
        class="oa-model-icon-choice"
        role="radio"
        :aria-checked="avatar === MARK_NONE"
        :class="{ active: avatar === MARK_NONE }"
        @click="choose(MARK_NONE)"
      >
        <ModelTile :avatar="MARK_NONE" :name="props.name || '?'" />
        <span>{{ t('modelIconInitials') }}</span>
      </button>
      <button
        type="button"
        class="oa-model-icon-choice"
        :class="{ active: isImage }"
        @click="file?.click()"
      >
        <ModelTile v-if="isImage" :avatar="avatar" :name="props.name" />
        <span v-else class="ai-model-tile"><IconImage :size="15" /></span>
        <span>{{ t('modelIconUpload') }}</span>
      </button>
      <input ref="file" type="file" accept="image/*" hidden @change="onFile">
    </div>
    <OaTextField
      v-model="link"
      :label="t('modelIconLink')"
      placeholder="/static/model.png"
      :hint="t('modelIconLinkHint')"
      monospace
    />
    <p v-if="error" class="oa-field-hint" role="alert">{{ error }}</p>
  </div>
</template>
