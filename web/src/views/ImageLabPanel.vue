<script setup lang="ts">
// Image Generation Lab panel.
//
// A side panel presented beside the chat, where users can select an image-capable
// model, provide a prompt, pick a visual style and aspect ratio, generate images
// and download the results directly.

import { computed, onMounted, onUnmounted, ref } from 'vue';
import { useRouter } from 'vue-router';
import { generateImages, type ImageGenerationItem } from '@/api/images';
import OaFormSection from '@/components/OaFormSection.vue';
import OaIconButton from '@/components/OaIconButton.vue';
import OaImageLightbox from '@/components/OaImageLightbox.vue';
import OaPanel from '@/components/OaPanel.vue';
import OaSelectField from '@/components/OaSelectField.vue';
import { t } from '@/composables/useI18n';
import { ImageError, prepareImage } from '@/chat/image';
import { loadModels, models } from '@/chat/useModels';
import { IconClose, IconDownload, IconImage } from '@/icons';

const router = useRouter();

const selectedModelID = ref('');
const prompt = ref('');
const selectedStyle = ref('');
const selectedSize = ref('1024x1024');

const busy = ref(false);
const error = ref('');
const history = ref<ImageGenerationItem[]>([]);
const zoomedImage = ref<{ url: string; alt?: string } | null>(null);

/**
 * The picture the prompt works from, downscaled in the browser by the same
 * code that prepares a chat attachment — a phone photo is four thousand
 * pixels wide, and the provider gains nothing from the other three thousand.
 */
const reference = ref<{ data: string; preview: string } | null>(null);
const picker = ref<HTMLInputElement | null>(null);

function dropReference(): void {
  if (!reference.value) return;
  URL.revokeObjectURL(reference.value.preview);
  reference.value = null;
}

async function takeReference(): Promise<void> {
  const node = picker.value;
  const file = node?.files?.[0] ?? null;
  // Cleared straight away so picking the same file twice still fires a change.
  if (node) node.value = '';
  if (!file) return;

  try {
    const prepared = await prepareImage(file);
    dropReference();
    reference.value = { data: prepared.data, preview: prepared.previewURL };
    error.value = '';
  } catch (err) {
    error.value = err instanceof ImageError ? err.message : t('imageFailed');
  }
}

// A blob: URL is held by the document until it is released, so leaving the
// panel with a picture in it would leak the whole downscaled image.
onUnmounted(dropReference);

const imageCapableModels = computed(() =>
  models.value.filter((m) => m.usable !== false && m.supports_image_gen),
);

// Only models that generate pictures: the endpoint refuses anything else, so
// offering a chat model here would be offering a choice that can only fail.
// It is the mirror of the composer's picker, which no longer lists these.
const modelOptions = computed(() => imageCapableModels.value.map((m) => ({
  value: m.id,
  label: m.display_name,
})));

const styleOptions = computed(() => [
  { value: '', label: t('imageStyleNone') },
  { value: 'vivid', label: t('styleVivid') },
  { value: 'natural', label: t('styleNatural') },
  { value: 'anime', label: t('styleAnime') },
  { value: 'photographic', label: t('stylePhotographic') },
  { value: 'cinematic', label: t('styleCinematic') },
  { value: 'digital-art', label: t('styleDigitalArt') },
  { value: 'watercolor', label: t('styleWatercolor') },
  { value: 'oil-painting', label: t('styleOilPainting') },
]);

// The sizes the two families of image models actually take: 1:1 and the two
// 3:2 sides are gpt-image-1's, the 16:9 pair is DALL-E 3's, and 4:3 is what
// several self-hosted models expect. Nothing here is validated server-side —
// the size is passed through to the provider — so `auto` is the escape hatch
// for a model whose own list is none of these.
const SIZES = [
  { value: '1024x1024', ratio: '1:1', w: 1, h: 1 },
  { value: '1536x1024', ratio: '3:2', w: 3, h: 2 },
  { value: '1024x1536', ratio: '2:3', w: 2, h: 3 },
  { value: '1152x896', ratio: '4:3', w: 4, h: 3 },
  { value: '896x1152', ratio: '3:4', w: 3, h: 4 },
  { value: '1792x1024', ratio: '16:9', w: 16, h: 9 },
  { value: '1024x1792', ratio: '9:16', w: 9, h: 16 },
  { value: '', ratio: '', w: 1, h: 1 },
] as const;

/**
 * The silhouette drawn on a tile, inside the 32-unit icon box.
 *
 * Bounded on the long side so a 16:9 fits, and on area so a square does not
 * tower over the wide ones — one rule for every ratio, which is what lets a
 * preset be added to the table above without drawing another `<svg>`.
 */
function silhouette(w: number, h: number) {
  const scale = Math.min(26 / Math.max(w, h), Math.sqrt(420 / (w * h)));
  const width = Math.round(w * scale * 10) / 10;
  const height = Math.round(h * scale * 10) / 10;
  return { width, height, x: (32 - width) / 2, y: (32 - height) / 2 };
}

const sizeOptions = computed(() => SIZES.map((size) => ({
  value: size.value,
  // The pixels rather than a word for the shape: with eight presets on screen
  // "portrait" no longer picks one out, and the dimensions are what a model's
  // own documentation lists.
  name: size.ratio || t('ratioAuto'),
  detail: size.value ? size.value.replace('x', ' \u00d7 ') : t('ratioAutoHint'),
  box: silhouette(size.w, size.h),
})));

onMounted(async () => {
  if (!models.value.length) {
    try {
      await loadModels();
    } catch {
      // models loading error handled globally
    }
  }
  if (!selectedModelID.value && modelOptions.value.length) {
    selectedModelID.value = modelOptions.value[0]?.value ?? '';
  }
});

async function generate(): Promise<void> {
  const text = prompt.value.trim();
  if (!text || !selectedModelID.value || busy.value) return;

  busy.value = true;
  error.value = '';

  try {
    const res = await generateImages({
      model_id: selectedModelID.value,
      prompt: text,
      style: selectedStyle.value,
      size: selectedSize.value,
      n: 1,
      ...(reference.value ? { image: reference.value.data } : {}),
    });

    if (res.images && res.images.length) {
      history.value = [...res.images, ...history.value];
    }
  } catch (err) {
    error.value = err instanceof Error ? err.message : t('failed');
  } finally {
    busy.value = false;
  }
}

function imageSource(img: ImageGenerationItem): string {
  if (img.url) return img.url;
  if (img.b64_json) return `data:image/png;base64,${img.b64_json}`;
  return '';
}
</script>

<template>
  <OaPanel
    :title="t('imageLab')"
    :confirm-label="busy ? t('generatingImage') : t('generateImage')"
    :confirmable="!!prompt.trim() && !!selectedModelID"
    :busy="busy"
    :error="error"
    :width="460"
    body-class="oa-image-lab-body"
    @close="router.replace('/')"
    @confirm="generate"
  >
    <p v-if="!modelOptions.length" class="oa-field-hint">
      {{ t('noImageCapableModel') }}
    </p>
    <OaSelectField
      v-else
      v-model="selectedModelID"
      :label="t('chooseModel')"
      :options="modelOptions"
    />

    <OaSelectField
      v-model="selectedStyle"
      :label="t('imageStyle')"
      :options="styleOptions"
    />

    <div class="oa-field">
      <label class="oa-field-label">{{ t('imageSize') }}</label>
      <div class="oa-ratio-grid">
        <button
          v-for="opt in sizeOptions"
          :key="opt.value"
          type="button"
          class="oa-ratio-tile"
          :class="{ active: selectedSize === opt.value }"
          @click="selectedSize = opt.value"
        >
          <div class="oa-ratio-icon-wrap">
            <svg class="oa-ratio-svg" viewBox="0 0 32 32" fill="none" xmlns="http://www.w3.org/2000/svg">
              <rect
                class="oa-ratio-rect"
                :class="{ auto: !opt.value }"
                :x="opt.box.x"
                :y="opt.box.y"
                :width="opt.box.width"
                :height="opt.box.height"
                rx="3.5"
              />
            </svg>
          </div>
          <span class="oa-ratio-name">{{ opt.name }}</span>
          <span class="oa-ratio-sub">{{ opt.detail }}</span>
        </button>
      </div>
    </div>

    <div class="oa-field">
      <label class="oa-field-label">{{ t('referenceImage') }}</label>
      <div v-if="reference" class="oa-reference">
        <img class="oa-reference-img" :src="reference.preview" alt="" :draggable="false">
        <OaIconButton class="oa-reference-remove" :label="t('removeImage')" @click="dropReference">
          <IconClose :size="12" />
        </OaIconButton>
      </div>
      <button v-else type="button" class="oa-reference-pick" :disabled="busy" @click="picker?.click()">
        <IconImage :size="15" />
        <span>{{ t('referenceImageAdd') }}</span>
      </button>
      <p class="oa-field-hint">{{ t('referenceImageHint') }}</p>
      <input
        ref="picker"
        class="ai-chat-file"
        type="file"
        accept="image/*"
        hidden
        @change="takeReference"
      >
    </div>

    <div class="oa-field">
      <label class="oa-field-label" for="image-lab-prompt">{{ t('imagePrompt') }}</label>
      <textarea
        id="image-lab-prompt"
        v-model="prompt"
        class="oa-field-input"
        :placeholder="t('promptPlaceholder')"
        rows="4"
        :disabled="busy"
        @keydown.ctrl.enter="generate"
        @keydown.meta.enter="generate"
      />
    </div>

    <div v-if="busy" class="ai-chat-pending" style="margin: 16px 0;">
      <span class="ai-chat-spinner" />
      <span>{{ t('generatingImage') }}</span>
    </div>

    <div v-if="history.length" class="oa-image-lab-results">
      <OaFormSection :title="t('imageResult')" />
      <div v-for="(img, idx) in history" :key="idx" class="oa-image-lab-card">
        <img
          :src="imageSource(img)"
          class="oa-image-lab-img"
          :alt="img.revised_prompt || prompt"
          @click="zoomedImage = { url: imageSource(img), alt: img.revised_prompt || prompt }"
        />
        <p v-if="img.revised_prompt" class="oa-field-hint" style="margin-top: 8px;">
          {{ img.revised_prompt }}
        </p>
        <div class="oa-image-lab-actions">
          <a
            :href="imageSource(img)"
            :download="`image-${Date.now()}-${idx}.png`"
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
    </div>

    <OaImageLightbox
      v-if="zoomedImage"
      :src="zoomedImage.url"
      :alt="zoomedImage.alt"
      @close="zoomedImage = null"
    />
  </OaPanel>
</template>
