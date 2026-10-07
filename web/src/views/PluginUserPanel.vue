<script setup lang="ts">
// A panel a plugin brings for everybody signed in (UserPanelSpec), drawn by
// the core in the shape of the feedback panel: what it is for, a form, and
// the reader's own earlier submissions under it.
//
// The plugin declares the controls and does the requests; every pixel is
// drawn here with the same components the core's own panels use, so a plugin
// cannot bring markup or a style of its own into the chat.

import { computed, onMounted, ref, watch } from 'vue';
import { useRouter } from 'vue-router';
import { ApiError } from '@/api/client';
import { prepareImage } from '@/chat/image';
import OaBadge from '@/components/OaBadge.vue';
import OaImageLightbox from '@/components/OaImageLightbox.vue';
import OaPanel from '@/components/OaPanel.vue';
import OaSwitchField from '@/components/OaSwitchField.vue';
import OaTextArea from '@/components/OaTextArea.vue';
import OaTextField from '@/components/OaTextField.vue';
import { t } from '@/composables/useI18n';
import { IconClose } from '@/icons';
import { userPanels } from '@/plugins/registry';
import type { UserFormControl, UserFormValues, UserRecord } from '@/plugins/types';

const props = defineProps<{ slug: string }>();

const router = useRouter();

// Looked up rather than fixed at setup: the plugins load after the first
// paint, and a reader who opened /x/<slug> from a bookmark gets here before
// the plugin that owns it has said anything.
const spec = computed(() => userPanels().find((panel) => panel.slug === props.slug) ?? null);

// Pictures are sent inside the JSON body, which the server caps at 4 MiB for
// a plugin's route; base64 is a third bigger than the bytes it carries.
const MAX_IMAGE_BYTES = 2_800_000;

const values = ref<UserFormValues>({});
const busy = ref(false);
const error = ref('');
const sent = ref('');
const mine = ref<UserRecord[]>([]);
const loaded = ref(false);
const viewing = ref('');

function reset(): void {
  const next: UserFormValues = {};
  for (const control of spec.value?.controls ?? []) {
    const fallback = spec.value?.defaults?.[control.key];
    if (control.kind === 'switch') next[control.key] = fallback === true;
    else if (control.kind === 'images') next[control.key] = [];
    else if (control.kind === 'choice') next[control.key] = typeof fallback === 'string' ? fallback : control.options[0]?.value ?? '';
    else next[control.key] = typeof fallback === 'string' ? fallback : '';
  }
  values.value = next;
}

function text(key: string): string {
  const value = values.value[key];
  return typeof value === 'string' ? value : '';
}

function images(key: string): string[] {
  const value = values.value[key];
  return Array.isArray(value) ? value : [];
}

function set(key: string, value: string | boolean | string[]): void {
  values.value = { ...values.value, [key]: value };
  sent.value = '';
}

async function pick(control: Extract<UserFormControl, { kind: 'images' }>, event: Event): Promise<void> {
  const input = event.target as HTMLInputElement;
  const files = Array.from(input.files ?? []);
  input.value = '';
  error.value = '';
  const have = images(control.key);
  if (have.length + files.length > control.max) {
    error.value = t('pluginImagesTooMany', { count: control.max });
    return;
  }
  const added: string[] = [];
  for (const file of files) {
    try {
      const image = await prepareImage(file);
      URL.revokeObjectURL(image.previewURL);
      added.push(`data:${image.mime};base64,${image.data}`);
    } catch (failure) {
      error.value = failure instanceof Error ? failure.message : t('failed');
      return;
    }
  }
  const next = [...have, ...added];
  if (next.reduce((sum, url) => sum + url.length, 0) > MAX_IMAGE_BYTES) {
    error.value = t('pluginImagesTooLarge');
    return;
  }
  set(control.key, next);
}

function drop(key: string, index: number): void {
  set(key, images(key).filter((_, at) => at !== index));
}

async function refresh(): Promise<void> {
  const list = spec.value?.mine;
  if (!list) return;
  try {
    mine.value = await list.load();
  } catch (failure) {
    error.value = failure instanceof ApiError ? failure.message : t('failed');
  } finally {
    loaded.value = true;
  }
}

async function submit(): Promise<void> {
  const panel = spec.value;
  if (!panel || busy.value) return;
  for (const control of panel.controls) {
    if ((control.kind === 'text' || control.kind === 'textarea') && control.required && !text(control.key).trim()) {
      error.value = t('pluginFieldRequired', { field: control.label() });
      return;
    }
  }
  busy.value = true;
  error.value = '';
  try {
    sent.value = await panel.submit.run(values.value);
    reset();
    await refresh();
  } catch (failure) {
    error.value = failure instanceof ApiError ? failure.message : t('failed');
  } finally {
    busy.value = false;
  }
}

// The same component is reused when one plugin panel is opened from another.
watch(spec, (panel, before) => {
  if (!panel || panel === before) return;
  reset();
  mine.value = [];
  loaded.value = false;
  void refresh();
}, { immediate: false });

onMounted(() => {
  reset();
  void refresh();
});
</script>

<template>
  <OaPanel
    :title="spec ? spec.title() : t('loading')"
    :confirm-label="spec?.submit.label() ?? ''"
    :confirmable="!!spec"
    :width="480"
    :busy="busy"
    :error="error"
    @close="router.push('/')"
    @confirm="submit"
  >
    <p v-if="!spec" class="oa-menu-empty">{{ t('pluginPanelMissing') }}</p>
    <template v-else>
      <p v-if="spec.intro" class="oa-field-hint">{{ spec.intro() }}</p>

      <template v-for="control in spec.controls" :key="control.key">
        <div v-if="control.kind === 'choice'" class="oa-field">
          <span class="oa-field-label">{{ control.label() }}</span>
          <div class="oa-segmented">
            <button
              v-for="option in control.options"
              :key="option.value"
              type="button"
              class="oa-segmented-option"
              :class="{ active: text(control.key) === option.value }"
              @click="set(control.key, option.value)"
            >{{ option.label() }}</button>
          </div>
          <span v-if="control.hint" class="oa-field-hint">{{ control.hint() }}</span>
        </div>

        <OaTextField
          v-else-if="control.kind === 'text'"
          :model-value="text(control.key)"
          :label="control.label()"
          :hint="control.hint?.()"
          :placeholder="control.placeholder?.()"
          :max-length="control.maxLength"
          @update:model-value="set(control.key, $event)"
        />

        <OaTextArea
          v-else-if="control.kind === 'textarea'"
          :model-value="text(control.key)"
          :label="control.label()"
          :hint="control.hint?.()"
          :placeholder="control.placeholder?.()"
          :rows="control.rows ?? 6"
          @update:model-value="set(control.key, $event)"
        />

        <OaSwitchField
          v-else-if="control.kind === 'switch'"
          :model-value="values[control.key] === true"
          :label="control.label()"
          :hint="control.hint?.()"
          @update:model-value="set(control.key, $event)"
        />

        <div v-else-if="control.kind === 'images'" class="oa-field">
          <span class="oa-field-label">{{ control.label() }}</span>
          <div class="oa-plugin-images">
            <span v-for="(url, index) in images(control.key)" :key="index" class="oa-plugin-image">
              <img :src="url" alt="" @click="viewing = url">
              <button type="button" class="oa-icon-btn" :title="t('pluginImagesRemove')" @click="drop(control.key, index)">
                <IconClose :size="12" />
              </button>
            </span>
            <label v-if="images(control.key).length < control.max" class="oa-btn">
              {{ t('pluginImagesAdd') }}
              <input type="file" accept="image/*" multiple hidden @change="pick(control, $event)">
            </label>
          </div>
          <span v-if="control.hint" class="oa-field-hint">{{ control.hint() }}</span>
        </div>
      </template>

      <p v-if="sent" class="oa-feedback-sent" role="status">{{ sent }}</p>

      <section v-if="spec.mine" class="oa-feedback-mine">
        <h3 class="oa-panel-section-title">{{ spec.mine.title() }}</h3>
        <p v-if="!loaded" class="oa-menu-empty">{{ t('loading') }}</p>
        <p v-else-if="!mine.length" class="oa-menu-empty">{{ spec.mine.empty() }}</p>
        <ul v-else class="oa-feedback-list">
          <li v-for="record in mine" :key="record.id">
            <div class="oa-feedback-item">
              <span class="oa-feedback-item-head">
                <span class="oa-feedback-item-title">{{ record.title }}</span>
                <OaBadge v-if="record.badge" :tone="record.badge.tone">{{ record.badge.label }}</OaBadge>
              </span>
              <span v-if="record.meta?.length" class="oa-feedback-item-meta">
                <span v-for="(fact, index) in record.meta" :key="index">{{ fact }}</span>
              </span>
              <span v-if="record.note" class="oa-field-hint">{{ record.note }}</span>
            </div>
          </li>
        </ul>
      </section>
    </template>
  </OaPanel>

  <OaImageLightbox v-if="viewing" :src="viewing" @close="viewing = ''" />
</template>
