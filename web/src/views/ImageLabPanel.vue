<script setup lang="ts">
// Image Generation Lab panel.
//
// A side panel presented beside the chat, where users can select an image-capable
// model, provide a prompt, pick a visual style and aspect ratio, generate images
// and download the results directly.

import { computed, onMounted, onUnmounted, ref, watch } from 'vue';
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
import OaFullscreenButton from '@/components/OaFullscreenButton.vue';
import OaImageLightbox from '@/components/OaImageLightbox.vue';
import OaPanel from '@/components/OaPanel.vue';
import OaSelectField from '@/components/OaSelectField.vue';
import OaTurnstile from '@/components/OaTurnstile.vue';
import { t } from '@/composables/useI18n';
import { usePanelExit } from '@/composables/usePanelExit';
import { usePanelFullscreen } from '@/composables/usePanelFullscreen';
import { ImageError, prepareImage } from '@/chat/image';
import { loadModels, models } from '@/chat/useModels';
import {
  IconCheck,
  IconClose,
  IconDownload,
  IconImage,
  IconPlus,
  IconSpark,
  IconTrash,
} from '@/icons';
import { apiErrorText } from '@/lib/api-error';
import { GUARD_DEADLINE_MS, GuardTimeout, withDeadline } from '@/lib/deadline';
import { relativeTime } from '@/lib/format';
import { solvePoW } from '@/lib/pow';
import { challengeText } from '@/lib/refusal';
import { guards } from '@/plugins/registry';
import { siteInfo } from '@/stores/session';

const panels = usePanelExit();
const panel = ref<InstanceType<typeof OaPanel> | null>(null);
const { fullscreen, toggle: toggleFullscreen } = usePanelFullscreen(() => panel.value);

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
const statusText = ref('');

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
// A result as the lab shows it. Its download name is worked out once, when the
// picture arrives: the template is drawn again on every change, and a name built
// from the clock there would be a different one each time. The address it is
// drawn from is worked out then too, and the base64 it may come from is not
// kept beside it: a data URI already is that picture, and two copies of every
// result would be held for the whole session.
interface LabResult extends Omit<ImageGenerationItem, 'b64_json'> {
  src: string;
  download: string;
}
const history = ref<LabResult[]>([]);
// The lightbox's download name travels with the picture it was opened on: the
// gallery and the results name their files differently, and a lightbox without
// one would fall back to .png for a JPEG.
const zoomedImage = ref<{ url: string; alt?: string; download?: string } | null>(null);

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
// Set when the panel closes: a picture still being downscaled then has no list
// to join, and its blob: URL would outlive clearReferences.
let unmounted = false;
// Slots promised to pictures still being downscaled. Counted with the pictures
// already in the list, so two additions in flight cannot both claim the same room.
let pendingReferences = 0;

async function addReferenceFiles(list: FileList | File[] | null | undefined): Promise<void> {
  const allFiles = Array.from(list ?? []);
  if (!allFiles.length) return;

  const imageFiles = allFiles.filter((file) => file.type.startsWith('image/'));
  if (!imageFiles.length) {
    error.value = t('imageNotAnImage');
    return;
  }

  // Cleared once per addition, so a refusal from an earlier batch does not outlive
  // a later one, but not inside the loop: a success there would wipe the message
  // for a picture that failed earlier in the same batch.
  error.value = '';

  const remaining = MAX_REFERENCE_IMAGES - references.value.length - pendingReferences;
  if (remaining <= 0) {
    error.value = t('referenceImageLimit', { count: MAX_REFERENCE_IMAGES });
    return;
  }

  if (imageFiles.length > remaining) {
    error.value = t('referenceImageLimit', { count: MAX_REFERENCE_IMAGES });
  }

  // The whole batch's slots are reserved before the first await, so a second
  // addition started meanwhile sees them as taken.
  const toProcess = imageFiles.slice(0, remaining);
  pendingReferences += toProcess.length;
  let reserved = toProcess.length;
  preparing.value = true;
  try {
    for (const file of toProcess) {
      try {
        const prepared = await prepareImage(file);
        if (unmounted) {
          URL.revokeObjectURL(prepared.previewURL);
          return;
        }
        references.value.push({
          id: `${Date.now()}-${Math.random().toString(36).slice(2, 7)}`,
          data: prepared.data,
          preview: prepared.previewURL,
        });
      } catch (err) {
        error.value = err instanceof ImageError ? err.message : t('imageFailed');
      } finally {
        pendingReferences -= 1;
        reserved -= 1;
      }
    }
  } finally {
    // Only what an early return left unprepared; the rest was given back above.
    pendingReferences -= reserved;
    // Another batch may still be preparing its pictures; clearing the flag
    // here would let generate() run without them.
    preparing.value = pendingReferences > 0;
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
onUnmounted(() => {
  unmounted = true;
  clearReferences();
});

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

// The silhouette sits in a 32-unit icon box. Both its longest side and its area
// are capped, so every ratio is drawn at a comparable size.
const ICON_BOX = 32;
const SILHOUETTE_MAX_SIDE = 26;
const SILHOUETTE_MAX_AREA = 420;

/**
 * The silhouette drawn on a tile, inside the 32-unit icon box.
 *
 * Bounded on the long side so a 16:9 fits, and on area so a square does not
 * tower over the wide ones — one rule for every ratio, which is what lets a
 * preset be added to the table above without drawing another `<svg>`.
 */
function silhouette(w: number, h: number) {
  const scale = Math.min(SILHOUETTE_MAX_SIDE / Math.max(w, h), Math.sqrt(SILHOUETTE_MAX_AREA / (w * h)));
  const width = Math.round(w * scale * 10) / 10;
  const height = Math.round(h * scale * 10) / 10;
  return { width, height, x: (ICON_BOX - width) / 2, y: (ICON_BOX - height) / 2 };
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


// A refresh asked for while a read is already running is kept, not dropped: that
// read may have started before the new picture was saved, so the refresh runs once
// it lands. A page of older results is still turned away, because two pages that
// both append would list the same rows twice.
let galleryRefreshWaiting = false;

async function loadGallery(before?: number, beforeId?: string): Promise<void> {
  if (loadingGallery.value) {
    if (!before) galleryRefreshWaiting = true;
    return;
  }
  loadingGallery.value = true;
  try {
    const res = await listImageGenerations(
      before ? { limit: 20, before, beforeId } : { limit: 20 },
    );
    if (before) {
      gallery.value.push(...res.generations);
    } else {
      gallery.value = res.generations;
    }
    galleryHasMore.value = res.generations.length === 20;
  } catch (err) {
    error.value = err instanceof ApiError ? apiErrorText(err) : t('failed');
  } finally {
    loadingGallery.value = false;
    if (galleryRefreshWaiting && !unmounted) {
      galleryRefreshWaiting = false;
      void loadGallery();
    }
  }
}

async function handleDelete(id: string): Promise<void> {
  try {
    await deleteImageGeneration(id);
    gallery.value = gallery.value.filter((item) => item.id !== id);
  } catch (err) {
    error.value = err instanceof ApiError ? apiErrorText(err) : t('failed');
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

  // Read before the first await: a solve or a guard can take seconds, and closing
  // the panel meanwhile clears the references. The request carries what was on
  // screen when Generate was pressed, not what is left of it afterwards.
  const modelID = selectedModelID.value;
  const style = selectedStyle.value;
  const size = selectedSize.value;
  // Only `images`: the server reads that list in preference to `image`, so
  // sending the first picture under both uploads it twice.
  const images = references.value.map((r) => r.data);

  busy.value = true;
  try {
    // Cheapest first, as the server asks them: the arithmetic, then the
    // plugin guards, whose service may take a moment.
    let pow: PoWSolution | undefined;
    if (needsPoW.value) {
      statusText.value = t('powSolving');
      // Not cancelled if the panel closes, and neither is the request after it:
      // the reader already pressed Generate, and the picture is kept in the
      // gallery either way.
      pow = await solvePoW(await fetchPoWChallenge()).promise;
    }
    const guardTokens: Record<string, string> = {};
    for (const { guard, config } of activeGuards.value) {
      statusText.value = guard.checking();
      try {
        guardTokens[guard.name] = await withDeadline(guard.token('images', config), GUARD_DEADLINE_MS);
      } catch (failure: unknown) {
        error.value = failure instanceof GuardTimeout ? t('guardTimeout') : guard.failed(failure);
        return;
      }
    }
    statusText.value = '';

    const res = await generateImages({
      model_id: modelID,
      prompt: text,
      style,
      size,
      n: 1,
      ...(images.length > 0 ? { images } : {}),
      ...(turnstileToken ? { turnstile: turnstileToken } : {}),
      ...(pow ? { pow } : {}),
      ...(Object.keys(guardTokens).length ? { guards: guardTokens } : {}),
    });

    if (res.images && res.images.length) {
      const arrived = Date.now();
      // The gallery URL is the copy the screen reads; a data URI stands in only
      // when the server gave no URL.
      const fresh = res.images.map((img, n): LabResult => {
        const { b64_json: bytes, ...rest } = img;
        return {
          ...rest,
          src: imageSource(img.url, bytes, img.mime),
          download: `image-${arrived}-${n}.${imageExtension(img.mime)}`,
        };
      });
      history.value = [...fresh, ...history.value];
      void loadGallery();
    }
  } catch (err) {
    error.value = challengeText(err) ?? (err instanceof ApiError ? apiErrorText(err) : t('failed'));
  } finally {
    statusText.value = '';
    busy.value = false;
    // Spent whether or not it passed.
    turnstile.value?.reset();
  }
}

// Both kinds of card open the same lightbox with the name their download link
// uses, so the two cannot drift apart.
function zoomResult(img: LabResult): void {
  zoomedImage.value = { url: img.src, alt: img.revised_prompt || prompt.value, download: img.download };
}

function zoomGalleryItem(item: ImageGenerationRecord): void {
  zoomedImage.value = { url: item.url, alt: item.revised_prompt || item.prompt, download: galleryDownloadName(item) };
}

function imageSource(url?: string, b64?: string, mime?: string): string {
  if (url) return url;
  if (b64) return `data:${mime || 'image/png'};base64,${b64}`;
  return '';
}

// The extension follows the bytes the provider sent: a JPEG saved as .png is
// mislabelled for whatever opens it. Anything unlisted keeps the name it always had.
function imageExtension(mime: string | undefined): string {
  switch (mime) {
    case 'image/jpeg':
      return 'jpg';
    case 'image/webp':
      return 'webp';
    case 'image/gif':
      return 'gif';
    default:
      return 'png';
  }
}

// A gallery picture is named by its row, not by when it arrived: the same
// picture has the same name on every visit to the gallery.
function galleryDownloadName(item: ImageGenerationRecord): string {
  return `image-${item.id}.${imageExtension(item.mime)}`;
}
</script>

<template>
  <OaPanel
    ref="panel"
    :title="t('imageLab')"
    :confirm-label="activeTab === 'generate' ? (busy ? (statusText || t('generatingImage')) : t('generateImage')) : undefined"
    :confirmable="activeTab === 'generate' && !!prompt.trim() && !!selectedModelID && !preparing"
    :footer="activeTab === 'generate'"
    :busy="busy"
    :error="error"
    :width="460"
    body-class="oa-image-lab-body"
    @close="panels.close('replace')"
    @confirm="generate"
  >
    <template #actions>
      <OaFullscreenButton :fullscreen="fullscreen" @toggle="toggleFullscreen" />
    </template>

    <div class="oa-slide-tabs oa-image-lab-tabs" role="tablist">
      <span
        class="oa-slide-pill"
        :class="{ second: activeTab === 'history' }"
        aria-hidden="true"
      />
      <button
        type="button"
        class="oa-slide-tab oa-image-lab-tab"
        :class="{ active: activeTab === 'generate' }"
        role="tab"
        :aria-selected="activeTab === 'generate'"
        @click="activeTab = 'generate'"
      >
        {{ t('imageLabTabGenerate') }}
      </button>
      <button
        type="button"
        class="oa-slide-tab oa-image-lab-tab"
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

        <div v-if="busy" class="ai-chat-pending oa-image-lab-pending">
          <span class="ai-chat-spinner" />
          <span>{{ statusText || t('generatingImage') }}</span>
        </div>

        <div v-if="history.length" class="oa-image-lab-results">
          <OaFormSection :title="t('imageResult')" />
          <div v-for="img in history" :key="img.download" class="oa-image-lab-card">
            <img
              :src="img.src"
              class="oa-image-lab-img"
              role="button"
              tabindex="0"
              :aria-label="t('zoomImage')"
              :alt="img.revised_prompt || prompt"
              @click="zoomResult(img)"
              @keydown.enter.prevent="zoomResult(img)"
              @keydown.space.prevent="zoomResult(img)"
            />
            <p v-if="img.revised_prompt" class="oa-field-hint oa-image-lab-note">
              {{ img.revised_prompt }}
            </p>
            <div class="oa-image-lab-actions">
              <a
                :href="img.src"
                :download="img.download"
                target="_blank"
                rel="noopener"
                class="oa-btn primary oa-image-lab-download"
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
      <div v-if="loadingGallery && !gallery.length" class="ai-chat-pending oa-image-lab-loading">
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
            role="button"
            tabindex="0"
            :aria-label="t('zoomImage')"
            @click="zoomGalleryItem(item)"
            @keydown.enter.prevent="zoomGalleryItem(item)"
            @keydown.space.prevent="zoomGalleryItem(item)"
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
                :download="galleryDownloadName(item)"
                target="_blank"
                rel="noopener"
                class="oa-gallery-btn"
                :title="t('downloadImage')"
                :aria-label="t('downloadImage')"
              >
                <IconDownload :size="12" />
              </a>
              <span class="oa-gallery-spacer" />
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
          @click="loadGallery(gallery[gallery.length - 1]?.created_at, gallery[gallery.length - 1]?.id)"
        >
          {{ loadingGallery ? t('thinking') : t('loadMoreImages') }}
        </button>
      </div>
    </template>

    <OaImageLightbox
      v-if="zoomedImage"
      :src="zoomedImage.url"
      :alt="zoomedImage.alt"
      :download-name="zoomedImage.download"
      @close="zoomedImage = null"
    />
  </OaPanel>
</template>
