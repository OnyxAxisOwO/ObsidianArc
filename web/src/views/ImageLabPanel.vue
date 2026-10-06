<script setup lang="ts">
// Image Generation Lab panel.
//
// A side panel presented beside the chat, where users can select an image-capable
// model, provide a prompt, pick a visual style and aspect ratio, generate images
// and download the results directly.

import { computed, onMounted, onUnmounted, ref, watch } from 'vue';
import { useRouter } from 'vue-router';
import {
  deleteImageGeneration,
  generateImages,
  listImageGenerations,
  type ImageGenerationItem,
  type ImageGenerationRecord,
} from '@/api/images';
import { fetchPoWChallenge, type PoWSolution } from '@/api/auth';
import { ApiError } from '@/api/client';
import OaConfirmButton from '@/components/OaConfirmButton.vue';
import OaFormSection from '@/components/OaFormSection.vue';
import OaIconButton from '@/components/OaIconButton.vue';
import OaImageLightbox from '@/components/OaImageLightbox.vue';
import OaPanel from '@/components/OaPanel.vue';
import OaSelectField from '@/components/OaSelectField.vue';
import OaTurnstile from '@/components/OaTurnstile.vue';
import { t } from '@/composables/useI18n';
import { ImageError, prepareImage } from '@/chat/image';
import { loadModels, models } from '@/chat/useModels';
import {
  IconCheck,
  IconClose,
  IconCollapse,
  IconDownload,
  IconExpand,
  IconImage,
  IconPlus,
  IconSpark,
  IconTrash,
} from '@/icons';
import { relativeTime } from '@/lib/format';
import { solvePoW } from '@/lib/pow';
import { guards } from '@/plugins/registry';
import { siteInfo } from '@/stores/session';

const router = useRouter();
const panel = ref<InstanceType<typeof OaPanel> | null>(null);
const fullscreen = ref(false);

const activeTab = ref<'generate' | 'history'>('generate');
const gallery = ref<ImageGenerationRecord[]>([]);
const loadingGallery = ref(false);
const galleryHasMore = ref(false);

const selectedModelID = ref('');
const prompt = ref('');
const selectedStyle = ref('');
const selectedSize = ref('1024x1024');

const busy = ref(false);
const error = ref('');
/** What the confirm button says while a challenge, not the model, is the wait. */
const stage = ref('');

// The operator's checks in front of the lab. Each token is good for one
// picture, so every attempt asks again, whatever became of the last.
const turnstile = ref<InstanceType<typeof OaTurnstile> | null>(null);
const needsTurnstile = computed(() =>
  !!siteInfo.value.turnstile_on_images && !!siteInfo.value.turnstile_site_key);
const needsPoW = computed(() => !!siteInfo.value.pow_on_images);
const activeGuards = computed(() =>
  guards().filter(({ guard, config }) => guard.active('images', config)));

// Prepared when the panel opens rather than on submit: a service that scores
// behaviour has nothing to score if it starts at submit time.
watch(activeGuards, (list) => {
  for (const { guard, config } of list) guard.prepare?.('images', config);
}, { immediate: true });
const history = ref<ImageGenerationItem[]>([]);
const zoomedImage = ref<{ url: string; alt?: string } | null>(null);

const MAX_REFERENCE_IMAGES = 5;

interface ReferenceItem {
  id: string;
  data: string;
  preview: string;
}

/**
 * Pictures the prompt works from, downscaled in the browser by the same
 * code that prepares a chat attachment — a phone photo is four thousand
 * pixels wide, and the provider gains nothing from the other three thousand.
 */
const references = ref<ReferenceItem[]>([]);
const picker = ref<HTMLInputElement | null>(null);

function dropReference(index: number): void {
  const item = references.value[index];
  if (!item) return;
  URL.revokeObjectURL(item.preview);
  references.value.splice(index, 1);
}

function clearReferences(): void {
  for (const item of references.value) {
    URL.revokeObjectURL(item.preview);
  }
  references.value = [];
}

const draggingRef = ref(false);
const preparing = ref(false);
let dragDepth = 0;

async function addReferenceFiles(list: FileList | File[] | null | undefined): Promise<void> {
  const allFiles = Array.from(list ?? []);
  if (!allFiles.length) return;

  const imageFiles = allFiles.filter((file) => file.type.startsWith('image/'));
  if (!imageFiles.length) {
    error.value = t('imageNotAnImage');
    return;
  }

  const remaining = MAX_REFERENCE_IMAGES - references.value.length;
  if (remaining <= 0) {
    error.value = t('referenceImageLimit', { count: MAX_REFERENCE_IMAGES });
    return;
  }

  if (imageFiles.length > remaining) {
    error.value = t('referenceImageLimit', { count: MAX_REFERENCE_IMAGES });
  }

  const toProcess = imageFiles.slice(0, remaining);
  preparing.value = true;
  try {
    for (const file of toProcess) {
      try {
        const prepared = await prepareImage(file);
        references.value.push({
          id: `${Date.now()}-${Math.random().toString(36).slice(2, 7)}`,
          data: prepared.data,
          preview: prepared.previewURL,
        });
        if (imageFiles.length <= remaining) {
          error.value = '';
        }
      } catch (err) {
        error.value = err instanceof ImageError ? err.message : t('imageFailed');
      }
    }
  } finally {
    preparing.value = false;
  }
}

async function takeReference(): Promise<void> {
  const node = picker.value;
  const files = Array.from(node?.files ?? []);
  // Cleared straight away so picking the same file twice still fires a change.
  if (node) node.value = '';
  await addReferenceFiles(files);
}

function carriesFiles(event: DragEvent): boolean {
  const types = event.dataTransfer?.types;
  return !!types && Array.prototype.indexOf.call(types, 'Files') !== -1;
}

function onDragEnter(event: DragEvent): void {
  if (!carriesFiles(event)) return;
  event.preventDefault();
  dragDepth++;
  draggingRef.value = true;
}

function onDragOver(event: DragEvent): void {
  if (!carriesFiles(event)) return;
  event.preventDefault();
  if (event.dataTransfer) event.dataTransfer.dropEffect = 'copy';
  draggingRef.value = true;
}

function onDragLeave(event: DragEvent): void {
  if (!carriesFiles(event)) return;
  dragDepth--;
  if (dragDepth <= 0) {
    dragDepth = 0;
    draggingRef.value = false;
  }
}

function onDrop(event: DragEvent): void {
  if (!carriesFiles(event)) return;
  event.preventDefault();
  event.stopPropagation();
  dragDepth = 0;
  draggingRef.value = false;
  void addReferenceFiles(event.dataTransfer?.files);
}

function onPaste(event: ClipboardEvent): void {
  if (activeTab.value !== 'generate') return;
  const items = event.clipboardData?.items;
  if (!items) return;
  const files: File[] = [];
  for (const item of Array.from(items)) {
    if (item.type.startsWith('image/')) {
      const file = item.getAsFile();
      if (file) files.push(file);
    }
  }
  if (files.length) {
    event.preventDefault();
    void addReferenceFiles(files);
  }
}

// A blob: URL is held by the document until it is released, so leaving the
// panel with pictures in it would leak the whole downscaled images.
onUnmounted(clearReferences);

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
  void loadGallery();
});

function toggleFullscreen(): void {
  fullscreen.value = panel.value?.toggleFullscreen() ?? false;
}

async function loadGallery(before?: number): Promise<void> {
  if (loadingGallery.value) return;
  loadingGallery.value = true;
  try {
    const res = await listImageGenerations(before ? { limit: 20, before } : { limit: 20 });
    if (before) {
      gallery.value.push(...res.generations);
    } else {
      gallery.value = res.generations;
    }
    galleryHasMore.value = res.generations.length === 20;
  } catch (err) {
    error.value = err instanceof Error ? err.message : t('failed');
  } finally {
    loadingGallery.value = false;
  }
}

async function handleDelete(id: string): Promise<void> {
  try {
    await deleteImageGeneration(id);
    gallery.value = gallery.value.filter((item) => item.id !== id);
  } catch (err) {
    error.value = err instanceof Error ? err.message : t('failed');
  }
}

function usePrompt(item: ImageGenerationRecord): void {
  prompt.value = item.prompt;
  if (item.style) {
    selectedStyle.value = item.style;
  }
  if (item.size) {
    selectedSize.value = item.size;
  }
  if (item.model_id && models.value.some((m) => m.id === item.model_id)) {
    selectedModelID.value = item.model_id;
  }
  activeTab.value = 'generate';
}

// A guard's SDK can take a while and has no deadline of its own, so a check
// that never answers would leave the button spinning for good.
const GUARD_DEADLINE_MS = 60_000;

class GuardTimeout extends Error {}

function withDeadline<T>(work: Promise<T>, ms: number): Promise<T> {
  return new Promise<T>((resolve, reject) => {
    const timer = window.setTimeout(() => reject(new GuardTimeout()), ms);
    work.then(
      (value) => { window.clearTimeout(timer); resolve(value); },
      (failure: unknown) => { window.clearTimeout(timer); reject(failure); },
    );
  });
}

async function generate(): Promise<void> {
  const text = prompt.value.trim();
  if (!text || !selectedModelID.value || busy.value || preparing.value) return;

  error.value = '';
  let turnstileToken = '';
  if (needsTurnstile.value) {
    turnstileToken = turnstile.value?.token() ?? '';
    if (!turnstileToken) {
      error.value = t('challengeRequired');
      return;
    }
  }

  busy.value = true;
  try {
    // Cheapest first, as the server asks them: the arithmetic, then the
    // plugin guards, whose service may take a moment.
    let pow: PoWSolution | undefined;
    if (needsPoW.value) {
      stage.value = t('powSolving');
      pow = await solvePoW(await fetchPoWChallenge()).promise;
    }
    const guardTokens: Record<string, string> = {};
    for (const { guard, config } of activeGuards.value) {
      stage.value = guard.checking();
      try {
        guardTokens[guard.name] = await withDeadline(guard.token('images', config), GUARD_DEADLINE_MS);
      } catch (failure: unknown) {
        error.value = failure instanceof GuardTimeout ? t('guardTimeout') : guard.failed(failure);
        return;
      }
    }
    stage.value = '';

    const images = references.value.map((r) => r.data);
    const res = await generateImages({
      model_id: selectedModelID.value,
      prompt: text,
      style: selectedStyle.value,
      size: selectedSize.value,
      n: 1,
      ...(images.length > 0 ? { images, image: images[0] } : {}),
      ...(turnstileToken ? { turnstile: turnstileToken } : {}),
      ...(pow ? { pow } : {}),
      ...(Object.keys(guardTokens).length ? { guards: guardTokens } : {}),
    });

    if (res.images && res.images.length) {
      history.value = [...res.images, ...history.value];
      void loadGallery();
    }
  } catch (err) {
    const code = err instanceof ApiError ? err.code : '';
    if (code === 'challenge_failed' || code.startsWith('pow_')) error.value = t('challengeFailed');
    else if (code === 'challenge_unavailable') error.value = t('challengeUnavailable');
    else error.value = err instanceof Error ? err.message : t('failed');
  } finally {
    stage.value = '';
    busy.value = false;
    // Spent whether or not it passed.
    turnstile.value?.reset();
  }
}

function imageSource(img: ImageGenerationItem): string {
  if (img.url) return img.url;
  if (img.b64_json) return `data:${img.mime || 'image/png'};base64,${img.b64_json}`;
  return '';
}
</script>

<template>
  <OaPanel
    ref="panel"
    :title="t('imageLab')"
    :confirm-label="activeTab === 'generate' ? (busy ? (stage || t('generatingImage')) : t('generateImage')) : undefined"
    :confirmable="activeTab === 'generate' && !!prompt.trim() && !!selectedModelID && !preparing"
    :footer="activeTab === 'generate'"
    :busy="busy"
    :error="error"
    :width="460"
    body-class="oa-image-lab-body"
    @close="router.replace('/')"
    @confirm="generate"
  >
    <template #actions>
      <OaIconButton
        class="oa-icon-btn"
        :label="t(fullscreen ? 'exitFullscreen' : 'fullscreen')"
        @click="toggleFullscreen"
      >
        <IconCollapse v-if="fullscreen" :size="16" />
        <IconExpand v-else :size="16" />
      </OaIconButton>
    </template>

    <div class="oa-image-lab-tabs" role="tablist">
      <span
        class="oa-image-lab-pill"
        :class="{ 'tab-history': activeTab === 'history' }"
        aria-hidden="true"
      />
      <button
        type="button"
        class="oa-image-lab-tab"
        :class="{ active: activeTab === 'generate' }"
        role="tab"
        :aria-selected="activeTab === 'generate'"
        @click="activeTab = 'generate'"
      >
        {{ t('imageLabTabGenerate') }}
      </button>
      <button
        type="button"
        class="oa-image-lab-tab"
        :class="{ active: activeTab === 'history' }"
        role="tab"
        :aria-selected="activeTab === 'history'"
        @click="activeTab = 'history'"
      >
        {{ t('imageLabTabGallery') }}
      </button>
    </div>

    <template v-if="activeTab === 'generate'">
      <div
        class="oa-image-lab-generate"
        @dragenter="onDragEnter"
        @dragover="onDragOver"
        @dragleave="onDragLeave"
        @drop="onDrop"
        @paste="onPaste"
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
          <label class="oa-field-label">
            <span>{{ t('referenceImage') }}</span>
            <span v-if="references.length" class="oa-reference-count">{{ references.length }}/{{ MAX_REFERENCE_IMAGES }}</span>
          </label>
          <div class="oa-reference-zone" :class="{ dragging: draggingRef }">
            <div
              v-if="!references.length"
              class="oa-reference-dropzone oa-reference-pick"
              :class="{ dragging: draggingRef, disabled: busy || preparing }"
              role="button"
              tabindex="0"
              :aria-label="t('referenceImageAdd')"
              @click="picker?.click()"
              @keydown.enter.prevent="picker?.click()"
              @keydown.space.prevent="picker?.click()"
            >
              <div class="oa-reference-dropzone-icon">
                <IconImage :size="20" />
              </div>
              <div class="oa-reference-dropzone-text">
                <strong>{{ draggingRef ? t('dropHint') : t('referenceImageDrop') }}</strong>
                <span>{{ t('referenceImageDropHint') }}</span>
              </div>
            </div>

            <div v-else class="oa-reference-list">
              <div v-for="(item, idx) in references" :key="item.id" class="oa-reference">
                <img class="oa-reference-img" :src="item.preview" alt="" :draggable="false">
                <OaIconButton class="oa-reference-remove" :label="t('removeImage')" @click.stop="dropReference(idx)">
                  <IconClose :size="12" />
                </OaIconButton>
              </div>
              <button
                v-if="references.length < MAX_REFERENCE_IMAGES"
                type="button"
                class="oa-reference-add-tile oa-reference-pick"
                :class="{ dragging: draggingRef }"
                :disabled="busy || preparing"
                :title="t('referenceImageAdd')"
                @click="picker?.click()"
              >
                <IconPlus :size="18" />
                <span>{{ t('referenceImageAdd') }}</span>
              </button>
              <div v-if="draggingRef" class="oa-reference-drop-overlay">
                <IconImage :size="18" />
                <span>{{ t('dropHint') }}</span>
              </div>
            </div>
          </div>

          <div v-if="preparing" class="oa-reference-preparing">
            <span class="ai-chat-spinner" />
            <span>{{ t('referenceImageProcessing') }}</span>
          </div>

          <p class="oa-field-hint">{{ t('referenceImageHint') }}</p>
          <input
            ref="picker"
            class="ai-chat-file"
            type="file"
            accept="image/*"
            multiple
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

        <OaTurnstile
          v-if="needsTurnstile"
          ref="turnstile"
          :site-key="siteInfo.turnstile_site_key ?? ''"
        />

        <div v-if="busy" class="ai-chat-pending" style="margin: 16px 0;">
          <span class="ai-chat-spinner" />
          <span>{{ stage || t('generatingImage') }}</span>
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
      </div>
    </template>

    <template v-else-if="activeTab === 'history'">
      <div v-if="loadingGallery && !gallery.length" class="ai-chat-pending" style="margin: 32px auto;">
        <span class="ai-chat-spinner" />
        <span>{{ t('thinking') }}</span>
      </div>

      <p v-else-if="!gallery.length" class="oa-gallery-empty">
        {{ t('noImageHistory') }}
      </p>

      <div v-else class="oa-gallery-grid">
        <div v-for="item in gallery" :key="item.id" class="oa-gallery-card">
          <div
            class="oa-gallery-img-wrap"
            @click="zoomedImage = { url: item.url, alt: item.revised_prompt || item.prompt }"
          >
            <img
              class="oa-gallery-img"
              :src="item.url"
              :alt="item.prompt"
              loading="lazy"
            />
          </div>
          <div class="oa-gallery-info">
            <div class="oa-gallery-prompt" :title="item.prompt">
              {{ item.prompt }}
            </div>
            <div class="oa-gallery-meta">
              <span>{{ item.size || t('ratioAuto') }}</span>
              <span>{{ relativeTime(item.created_at) }}</span>
            </div>
            <div class="oa-gallery-actions">
              <button
                type="button"
                class="oa-gallery-btn"
                :title="t('usePrompt')"
                @click="usePrompt(item)"
              >
                <IconSpark :size="12" />
                <span>{{ t('usePrompt') }}</span>
              </button>
              <a
                :href="item.url"
                :download="`image-${item.id}.png`"
                target="_blank"
                rel="noopener"
                class="oa-gallery-btn"
                :title="t('downloadImage')"
                :aria-label="t('downloadImage')"
              >
                <IconDownload :size="12" />
              </a>
              <span style="flex: 1;" />
              <OaConfirmButton
                class="oa-gallery-btn danger"
                :resting-title="t('deleteImageConfirm')"
                :armed-title="t('deleteImageConfirm')"
                @confirm="handleDelete(item.id)"
              >
                <IconTrash :size="12" />
                <template #armed><IconCheck :size="12" /></template>
              </OaConfirmButton>
            </div>
          </div>
        </div>
      </div>

      <div v-if="galleryHasMore" class="oa-gallery-load-more">
        <button
          type="button"
          class="oa-btn"
          :disabled="loadingGallery"
          @click="loadGallery(gallery[gallery.length - 1]?.created_at)"
        >
          {{ loadingGallery ? t('thinking') : t('loadMoreImages') }}
        </button>
      </div>
    </template>

    <OaImageLightbox
      v-if="zoomedImage"
      :src="zoomedImage.url"
      :alt="zoomedImage.alt"
      @close="zoomedImage = null"
    />
  </OaPanel>
</template>
