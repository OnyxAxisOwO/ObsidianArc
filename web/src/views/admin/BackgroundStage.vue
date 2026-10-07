<script setup lang="ts">
// One set of four backgrounds — desktop and phone, light and dark — shown one
// at a time on a stage the shape of the screen it is for.
//
// It used to be four tiles side by side, each with its own buttons, and the
// card read as a form to fill in rather than a picture to look at. Now two
// small switches pick the screen, the stage shows it large, with a faint
// outline of the interface that will sit on top of it, and three quiet
// actions sit under it. A variant that is not set says which one stands in
// for it, which is the question an operator looking at an empty stage has.
//
// Used twice on the settings page, for the signed-out screens and for the
// signed-in interface: the sets differ only in the prefix their variants
// carry on the server and in what is drawn over them.

import { computed, ref } from 'vue';
import { Code, Monitor, Smartphone, Trash2, Upload } from 'lucide-vue-next';
import { adminApi } from '@/admin/api';
import { ApiError } from '@/api/client';
import OaIconButton from '@/components/OaIconButton.vue';
import OaOverlay from '@/components/OaOverlay.vue';
import { t, type StringKey } from '@/composables/useI18n';
import { useTheme } from '@/composables/useTheme';
import { IconClose, IconImage, IconMoon, IconSun } from '@/icons';
import { pickBackground, type SiteBackground } from '@/theme/theme';

const props = withDefaults(defineProps<{
  prefix: '' | 'app_';
  backgrounds: Record<string, SiteBackground>;
  /** Where the sign-in card sits, for the outline drawn over the signed-out set. */
  cardPosition?: string;
  /** How the signed-in set is dimmed and seen through, so the sliders preview here. */
  look?: { dim: number; blur: number; translucency: number; panelBlur: number } | undefined;
}>(), { cardPosition: 'center', look: undefined });

const emit = defineEmits<{
  (event: 'changed', variant: string, value: SiteBackground | null): void;
  (event: 'status', message: string, ok: boolean): void;
}>();

const LABELS: Record<string, StringKey> = {
  landscape_light: 'loginBgLandscapeLight',
  landscape_dark: 'loginBgLandscapeDark',
  portrait_light: 'loginBgPortraitLight',
  portrait_dark: 'loginBgPortraitDark',
};

const theme = useTheme();
const portrait = ref(false);
// Starts on the scheme the operator is looking at the page in: that is the
// one they can judge.
const dark = ref(theme.dark());

const variant = computed(() => `${portrait.value ? 'portrait' : 'landscape'}_${dark.value ? 'dark' : 'light'}`);
const key = computed(() => props.prefix + variant.value);
const current = computed<SiteBackground | undefined>(() => props.backgrounds[key.value]);
// What a reader on this screen actually gets: this variant, or the one the
// fallback lands on.
const shown = computed(() => pickBackground(props.backgrounds, props.prefix, portrait.value, dark.value));
const standIn = computed(() => {
  if (current.value || !shown.value) return '';
  const found = Object.entries(props.backgrounds)
    .find(([name, bg]) => name.startsWith(props.prefix) && bg.url === shown.value?.url);
  const bare = found ? found[0].slice(props.prefix.length) : '';
  return LABELS[bare] ? t(LABELS[bare]!) : '';
});
const status = computed(() => {
  if (current.value) return current.value.html ? t('bgKindHtml') : t('bgKindImage');
  if (standIn.value) return t('bgStandIn', { name: standIn.value });
  return props.prefix ? t('bgNoneSignedIn') : t('bgNoneSignedOut');
});

const stageStyle = computed(() => {
  const look = props.look;
  if (!look) return {};
  return {
    '--stage-dim': String(look.dim / 100),
    '--stage-blur': `${look.blur * 0.4}px`,
    '--stage-surface': `${100 - look.translucency}%`,
    '--stage-panel-blur': look.panelBlur > 0 ? `blur(${look.panelBlur * 0.5}px)` : 'none',
  };
});

const busy = ref(false);
const dragging = ref(false);
const editing = ref(false);
const draft = ref('');

function detectFileType(file: File): string {
  const type = file.type.toLowerCase();
  if (type === 'image/jpeg' || type === 'image/jpg') return 'image/jpeg';
  if (type === 'image/png') return 'image/png';
  if (type === 'image/webp') return 'image/webp';
  if (type === 'image/avif') return 'image/avif';
  const name = file.name.toLowerCase();
  if (name.endsWith('.jpg') || name.endsWith('.jpeg')) return 'image/jpeg';
  if (name.endsWith('.png')) return 'image/png';
  if (name.endsWith('.webp')) return 'image/webp';
  if (name.endsWith('.avif')) return 'image/avif';
  return '';
}

function fail(failure: unknown): void {
  emit('status', failure instanceof ApiError ? failure.message : String(failure), false);
}

async function uploadFile(file: File): Promise<void> {
  const mime = detectFileType(file);
  if (!mime || file.size > 6 * 1024 * 1024) {
    emit('status', t('loginBgFormats'), false);
    return;
  }
  const target = key.value;
  busy.value = true;
  try {
    const base64 = await new Promise<string>((resolve, reject) => {
      const reader = new FileReader();
      reader.onload = () => {
        const res = String(reader.result ?? '');
        const comma = res.indexOf(',');
        resolve(comma !== -1 ? res.slice(comma + 1) : res);
      };
      reader.onerror = () => reject(new Error('Failed to read file'));
      reader.readAsDataURL(file);
    });
    const res = await adminApi.uploadLoginBackground(target, mime, base64);
    emit('changed', target, { url: res.url, html: false });
    emit('status', t('loginBgUploaded'), true);
  } catch (failure) {
    fail(failure);
  } finally {
    busy.value = false;
  }
}

function choose(): void {
  const input = document.createElement('input');
  input.type = 'file';
  input.accept = 'image/png,image/jpeg,image/webp,image/avif,.jpg,.jpeg,.png,.webp,.avif';
  input.onchange = () => {
    const file = input.files?.[0];
    if (file) void uploadFile(file);
  };
  input.click();
}

function onDrop(event: DragEvent): void {
  dragging.value = false;
  const file = event.dataTransfer?.files?.[0];
  if (file) void uploadFile(file);
}

async function openEditor(): Promise<void> {
  editing.value = true;
  draft.value = '';
  const bg = current.value;
  if (!bg?.html) return;
  // The stored page, so editing starts from it rather than from nothing.
  try {
    const res = await fetch(bg.url, { credentials: 'omit' });
    if (res.ok && editing.value) draft.value = await res.text();
  } catch {
    // An empty editor is still an editor; saving replaces what was there.
  }
}

async function saveHTML(): Promise<void> {
  const target = key.value;
  busy.value = true;
  try {
    const res = await adminApi.uploadBackgroundHTML(target, draft.value);
    emit('changed', target, { url: res.url, html: true });
    emit('status', t('bgHtmlSaved'), true);
    editing.value = false;
  } catch (failure) {
    fail(failure);
  } finally {
    busy.value = false;
  }
}

async function clear(): Promise<void> {
  const target = key.value;
  busy.value = true;
  try {
    await adminApi.deleteLoginBackground(target);
    emit('changed', target, null);
    emit('status', t('loginBgDeleted'), true);
  } catch (failure) {
    fail(failure);
  } finally {
    busy.value = false;
  }
}
</script>

<template>
  <div class="oa-bg-stage-block" :style="stageStyle">
    <div class="oa-bg-stage-switches">
      <div class="oa-segment" role="group" :aria-label="t('bgScreen')">
        <button type="button" :aria-pressed="!portrait" @click="portrait = false">
          <Monitor :size="13" />{{ t('bgDesktop') }}
        </button>
        <button type="button" :aria-pressed="portrait" @click="portrait = true">
          <Smartphone :size="13" />{{ t('bgPhone') }}
        </button>
      </div>
      <div class="oa-segment" role="group" :aria-label="t('bgScheme')">
        <button type="button" :aria-pressed="!dark" @click="dark = false">
          <IconSun :size="13" />{{ t('themeLight') }}
        </button>
        <button type="button" :aria-pressed="dark" @click="dark = true">
          <IconMoon :size="13" />{{ t('themeDark') }}
        </button>
      </div>
    </div>

    <div
      class="oa-bg-stage"
      :class="{ portrait, dark, dragging, empty: !shown, 'stand-in': !current && !!shown, signed: !!look }"
      @dragover.prevent="dragging = true"
      @dragleave="dragging = false"
      @drop.prevent="onDrop"
    >
      <!-- A page previews as itself, in the same sandbox it runs in. -->
      <iframe
        v-if="shown?.html"
        :key="shown.url"
        class="oa-bg-stage-art"
        :src="shown.url"
        sandbox="allow-scripts"
        referrerpolicy="no-referrer"
        tabindex="-1"
        aria-hidden="true"
        title=""
      />
      <img v-else-if="shown" class="oa-bg-stage-art" :src="shown.url" alt="">
      <span v-if="look && shown" class="oa-bg-stage-dim" />

      <!-- The interface that will sit on it, as an outline: enough to judge
           contrast and placement, not a second copy of the screen. -->
      <div v-if="look" class="oa-bg-stage-ghost app" aria-hidden="true">
        <span class="rail" /><span class="main"><i /><i /><i class="composer" /></span>
      </div>
      <div v-else class="oa-bg-stage-ghost login" :class="`at-${cardPosition}`" aria-hidden="true">
        <span class="card"><i /><i /><i class="button" /></span>
      </div>

      <button v-if="!shown" type="button" class="oa-bg-stage-empty" @click="choose">
        <IconImage :size="22" />
        <span>{{ t('loginBgDropHint') }}</span>
        <small>{{ t('loginBgFormats') }}</small>
      </button>
    </div>

    <div class="oa-bg-stage-foot">
      <span class="oa-bg-stage-status" :class="{ set: !!current }">
        <span class="dot" />{{ status }}
      </span>
      <div class="oa-bg-stage-actions">
        <button type="button" class="oa-btn small" :disabled="busy" @click="choose">
          <Upload :size="13" />{{ current && !current.html ? t('loginBgReplace') : t('loginBgUpload') }}
        </button>
        <button type="button" class="oa-btn small" :disabled="busy" @click="openEditor">
          <Code :size="13" />{{ t('bgHtmlEdit') }}
        </button>
        <OaIconButton v-if="current" class="oa-icon-btn" :label="t('remove')" :disabled="busy" @click="clear">
          <Trash2 :size="14" />
        </OaIconButton>
      </div>
    </div>
  </div>

  <OaOverlay v-if="editing" overlay-class="oa-modal-overlay" @close="editing = false">
    <div class="oa-auth-card oa-modal-card oa-bg-html-editor">
      <OaIconButton class="oa-icon-btn oa-modal-close" :label="t('close')" @click="editing = false">
        <IconClose :size="16" />
      </OaIconButton>
      <span class="oa-bg-html-kicker">{{ t(LABELS[variant]!) }}</span>
      <h1 class="oa-auth-title">{{ t('bgHtmlTitle') }}</h1>
      <p class="oa-auth-sub">{{ t('bgHtmlHint') }}</p>
      <textarea
        v-model="draft"
        class="oa-bg-html-source"
        spellcheck="false"
        :aria-label="t('bgHtmlTitle')"
        placeholder="<!doctype html>&#10;<style>&#10;  body { margin: 0; background: linear-gradient(135deg, #6C4CD6, #00696E); }&#10;</style>"
      />
      <div class="oa-bg-html-actions">
        <button type="button" class="oa-btn" @click="editing = false">{{ t('cancel') }}</button>
        <button type="button" class="oa-btn primary" :disabled="!draft.trim() || busy" @click="saveHTML">
          {{ t('save') }}
        </button>
      </div>
    </div>
  </OaOverlay>
</template>
