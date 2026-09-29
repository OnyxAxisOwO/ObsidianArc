<script setup lang="ts">
// A backoffice page a plugin brings (AdminPluginPage), drawn by the core.
//
// The plugin declares where its cards and lists go — `plugin:<slug>` — and
// this page does what AdminBackup does for its own: loads the settings those
// cards show with the rest of the instance's, keeps a draft, says whether
// it is saved, and saves through the same endpoint. The plugin writes no
// markup and imports nothing from this chunk.

import { computed, onMounted, ref } from 'vue';
import { adminApi } from '@/admin/api';
import { ApiError } from '@/api/client';
import { t } from '@/composables/useI18n';
import type { AdminPluginPage } from '@/plugins/types';
import AdminFailure from './AdminFailure.vue';
import PluginList from './PluginList.vue';
import PluginSettingsCard from './PluginSettingsCard.vue';
import { usePluginSettings } from './pluginSettings';
import { plugins } from '@/plugins/registry';
import { useAdminView } from './adminView';

const props = defineProps<{ page: AdminPluginPage }>();

const view = useAdminView();
view.setTitle(props.page.title(), props.page.hint?.());

const placement = `plugin:${props.page.slug}` as const;
const settings = usePluginSettings(placement);
const lists = computed(() =>
  plugins().flatMap((plugin) => plugin.lists ?? []).filter((list) => list.page === placement));

const loaded = ref(false);
const error = ref('');
const busy = ref(false);
const saveError = ref('');
const baseline = ref('');

const dirty = computed(() => loaded.value && baseline.value !== JSON.stringify(settings.collect()));

function message(failure: unknown): string {
  return failure instanceof ApiError ? failure.message : String(failure);
}

async function load(): Promise<void> {
  error.value = '';
  if (!settings.sections.value.length) {
    loaded.value = true;
    return;
  }
  try {
    const { settings: values } = await adminApi.settings();
    settings.load(values);
    baseline.value = JSON.stringify(settings.collect());
    loaded.value = true;
  } catch (failure) {
    error.value = message(failure);
  }
}

async function save(): Promise<void> {
  busy.value = true;
  saveError.value = '';
  try {
    const { settings: values } = await adminApi.saveSettings(settings.collect());
    settings.load(values);
    baseline.value = JSON.stringify(settings.collect());
  } catch (failure) {
    saveError.value = message(failure);
  } finally {
    busy.value = false;
  }
}

onMounted(load);
</script>

<template>
  <Teleport :to="view.actionsHost">
    <template v-if="loaded && settings.sections.value.length">
      <span class="oa-control-save-state" :class="{ dirty }" role="status">
        <span class="oa-dashboard-dot" />{{ dirty ? t('controlUnsaved') : t('controlSaved') }}
      </span>
      <button type="button" class="oa-btn primary" :disabled="busy || !dirty" @click="save">
        {{ busy ? t('saving') : t('save') }}
      </button>
    </template>
  </Teleport>

  <AdminFailure v-if="error" :message="error" @retry="load" />
  <p v-else-if="!loaded" class="oa-table-empty">{{ t('loading') }}</p>
  <div v-else class="oa-workbench">
    <p v-if="saveError" class="oa-field-hint" role="alert">{{ saveError }}</p>
    <div v-if="settings.sections.value.length" class="oa-workbench-grid">
      <PluginSettingsCard
        v-for="section in settings.sections.value"
        :key="section.id"
        :section="section"
        :draft="settings.draft"
        :hints="settings.hints"
      />
    </div>
    <PluginList v-for="spec in lists" :key="spec.id" :spec="spec" />
  </div>
</template>
