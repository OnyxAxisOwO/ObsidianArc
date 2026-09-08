<script setup lang="ts">
// Image Generation Lab panel.
//
// A side panel presented beside the chat, where users can select an image-capable
// model, provide a prompt, pick a visual style and aspect ratio, generate images
// and download the results directly.

import { computed, onMounted, ref } from 'vue';
import { useRouter } from 'vue-router';
import { generateImages, type ImageGenerationItem } from '@/api/images';
import OaFormSection from '@/components/OaFormSection.vue';
import OaImageLightbox from '@/components/OaImageLightbox.vue';
import OaPanel from '@/components/OaPanel.vue';
import OaSelectField from '@/components/OaSelectField.vue';
import { t } from '@/composables/useI18n';
import { loadModels, models } from '@/chat/useModels';
import { IconDownload } from '@/icons';

const router = useRouter();

const selectedModelID = ref('');
const prompt = ref('');
const selectedStyle = ref('');
const selectedSize = ref('1024x1024');

const busy = ref(false);
const error = ref('');
const history = ref<ImageGenerationItem[]>([]);
const zoomedImage = ref<{ url: string; alt?: string } | null>(null);

const imageCapableModels = computed(() =>
  models.value.filter((m) => m.usable !== false && m.supports_image_gen),
);

const modelOptions = computed(() => {
  const source = imageCapableModels.value.length ? imageCapableModels.value : models.value.filter((m) => m.usable !== false);
  return source.map((m) => ({
    value: m.id,
    label: m.supports_image_gen ? `${m.display_name} (${t('canImageGen')})` : m.display_name,
  }));
});

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

const sizeOptions = computed(() => [
  { value: '1024x1024', ratio: '1:1', label: t('ratioSquare'), boxClass: 'square' },
  { value: '1024x1792', ratio: '9:16', label: t('ratioPortrait'), boxClass: 'portrait' },
  { value: '1792x1024', ratio: '16:9', label: t('ratioLandscape'), boxClass: 'landscape' },
]);

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
    @close="router.replace('/')"
    @confirm="generate"
  >
    <OaFormSection :title="t('imageLabSettings')" />

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
          <div class="oa-ratio-preview">
            <div class="oa-ratio-box" :class="opt.boxClass" />
          </div>
          <span class="oa-ratio-name">{{ opt.ratio }}</span>
          <span class="oa-ratio-sub">{{ opt.label }}</span>
        </button>
      </div>
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
