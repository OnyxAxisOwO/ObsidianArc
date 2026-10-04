<script setup lang="ts">
// Announcements: what a signed-in user finds in the bell menu.
//
// Draft and pinned are ordinary flags rather than a status enum, because an
// announcement can be either, both or neither, and enumerating the
// combinations would just be these two booleans wearing a costume.

import { computed, nextTick, onMounted, ref, watch } from 'vue';
import { useRoute } from 'vue-router';
import { adminApi, type Announcement } from '@/admin/api';
import { ApiError } from '@/api/client';
import OaBadge from '@/components/OaBadge.vue';
import OaBadgeRow from '@/components/OaBadgeRow.vue';
import OaBulkBar from '@/components/OaBulkBar.vue';
import OaCellStack from '@/components/OaCellStack.vue';
import OaNumberField from '@/components/OaNumberField.vue';
import OaPanel from '@/components/OaPanel.vue';
import OaSelectField from '@/components/OaSelectField.vue';
import OaSwitchField from '@/components/OaSwitchField.vue';
import OaTable from '@/components/OaTable.vue';
import OaTextArea from '@/components/OaTextArea.vue';
import OaTextField from '@/components/OaTextField.vue';
import type { Column } from '@/components/table-types';
import { t } from '@/composables/useI18n';
import { useBulk } from '@/composables/useBulk';
import { usePanelSlot } from '@/composables/usePanelSlot';
import { relativeTime } from '@/lib/format';
import AdminFailure from './AdminFailure.vue';
import { useAdminView } from './adminView';

type DisplayMode = Announcement['display_mode'];

const route = useRoute();
const view = useAdminView();
view.setTitle(t('announcements'), t('announcementsSubtitle'));

const announcements = ref<Announcement[]>([]);
const error = ref('');
const loaded = ref(false);

const editing = ref<Announcement | null>(null);
const creating = ref(false);
const busy = ref(false);
const panelError = ref('');

const form = ref({
  title: '',
  body: '',
  display_mode: 'once' as DisplayMode,
  dismiss_after_seconds: 0 as number | null,
  published: false,
  pinned: false,
});

const titleField = ref<InstanceType<typeof OaTextField> | null>(null);

const columns = computed<Array<Column<Announcement>>>(() => [
  { key: 'title', header: t('colTitle') },
  { key: 'appears', header: t('colAppears'), width: '130px' },
  { key: 'state', header: t('colState2'), width: '120px' },
  { key: 'updated', header: t('colUpdated'), text: (row) => relativeTime(row.updated_at), secondary: true, width: '110px' },
]);

/** The first ~60 characters of the body, for the table's second line. */
function preview(body: string): string {
  const collapsed = body.replace(/\s+/g, ' ').trim();
  return collapsed.length > 60 ? `${collapsed.slice(0, 60)}…` : collapsed;
}

function modeLabel(mode: DisplayMode): string {
  switch (mode) {
    case 'always': return t('displayAlways');
    case 'once': return t('displayOnce');
    default: return t('displaySilent');
  }
}

function open(record: Announcement | null): void {
  creating.value = record === null;
  editing.value = record;
  panelError.value = '';
  form.value = {
    title: record?.title ?? '',
    body: record?.body ?? '',
    display_mode: record?.display_mode ?? 'once',
    dismiss_after_seconds: record?.dismiss_after_seconds ?? 0,
    published: record?.published ?? false,
    pinned: record?.pinned ?? false,
  };
  // Saving leaves this set while the panel slides out.
  busy.value = false;
  showPanel();
  void nextTick(() => titleField.value?.focus({ preventScroll: true }));
}

const { open: panelOpen, panel, key: panelKey, show: showPanel, hide: hidePanel, closed: panelClosed } = usePanelSlot();

/**
 * The panel slides away and the table is refetched where it stands, rather
 * than the page being mounted again — which put the reader back at the top.
 */
function finish(): void {
  hidePanel();
  void load();
}

// --- several at once ----------------------------------------------------------------

const bulk = useBulk(() => announcements.value, (row) => row.id);

// The update replaces the whole announcement, so the row's own fields go back
// with the one that changed. A partial body would blank the title.
function bulkPublish(on: boolean): Promise<void> {
  return bulk.run((row) => (row.published === on ? Promise.resolve() : adminApi.updateAnnouncement(row.id, {
    title: row.title,
    body: row.body,
    display_mode: row.display_mode,
    dismiss_after_seconds: row.dismiss_after_seconds,
    published: on,
    pinned: row.pinned,
  })), load);
}

function bulkRemove(): Promise<void> {
  return bulk.run((row) => adminApi.deleteAnnouncement(row.id), load);
}

async function save(): Promise<void> {
  busy.value = true;
  panelError.value = '';
  const payload = {
    title: form.value.title.trim(),
    body: form.value.body.trim(),
    display_mode: form.value.display_mode,
    dismiss_after_seconds: form.value.dismiss_after_seconds ?? 0,
    published: form.value.published,
    pinned: form.value.pinned,
  };
  try {
    if (creating.value) await adminApi.createAnnouncement(payload);
    else await adminApi.updateAnnouncement(editing.value!.id, payload);
    finish();
  } catch (failure) {
    busy.value = false;
    panelError.value = failure instanceof ApiError ? failure.message : String(failure);
  }
}

async function remove(): Promise<void> {
  const record = editing.value;
  if (!record) return;
  busy.value = true;
  try {
    await adminApi.deleteAnnouncement(record.id);
    finish();
  } catch (failure) {
    busy.value = false;
    panelError.value = failure instanceof ApiError ? failure.message : String(failure);
  }
}

async function load(): Promise<void> {
  // Only the first load may open the panel from the address's hash: this runs
  // again after every save, with the hash still there.
  const first = !loaded.value;
  error.value = '';
  try {
    ({ announcements: announcements.value } = await adminApi.announcements());
  } catch (failure) {
    error.value = failure instanceof Error ? failure.message : String(failure);
  } finally {
    loaded.value = true;
    if (first) checkDrawerTarget();
  }
}

function checkDrawerTarget(): void {
  if (route.hash === '#addAnnouncement' && !panelOpen.value) {
    open(null);
  }
}

watch(() => route.hash, () => {
  if (loaded.value) {
    checkDrawerTarget();
  }
});

onMounted(load);
</script>

<template>
  <Teleport :to="view.actionsHost">
    <button id="addAnnouncement" type="button" class="oa-btn primary" @click="open(null)">{{ t('addAnnouncement') }}</button>
  </Teleport>

  <AdminFailure v-if="error" :message="error" @retry="load" />
  <p v-else-if="!loaded" class="oa-table-empty">{{ t('loading') }}</p>

  <template v-else>
  <OaTable
    id="announcementsList"
    :columns="columns"
    :rows="announcements"
    :empty="t('announcementsEmpty')"
    :muted="(row) => !row.published"
    selectable
    multi
    v-model:selected="bulk.selected.value"
    :row-key="(row) => row.id"
    @select="open($event)"
  >
    <template #cell-title="{ row }">
      <OaCellStack :title="row.title" :sub="preview(row.body)" />
    </template>
    <template #cell-appears="{ row }">
      <OaBadge tone="muted">{{ modeLabel(row.display_mode) }}</OaBadge>
    </template>
    <template #cell-state="{ row }">
      <OaBadgeRow>
        <OaBadge v-if="!row.published" tone="danger">{{ t('draftBadge') }}</OaBadge>
        <OaBadge v-if="row.pinned" tone="muted">{{ t('pinnedBadge') }}</OaBadge>
      </OaBadgeRow>
    </template>
  </OaTable>
  <OaBulkBar
    :count="bulk.selected.value.length"
    :total="announcements.length"
    :busy="bulk.busy.value"
    :error="bulk.error.value"
    deletable
    @all="bulk.selectAll"
    @clear="bulk.clear"
    @delete="bulkRemove"
  >
    <button type="button" class="oa-btn small" :disabled="bulk.busy.value" @click="bulkPublish(true)">{{ t('bulkPublish') }}</button>
    <button type="button" class="oa-btn small" :disabled="bulk.busy.value" @click="bulkPublish(false)">{{ t('bulkUnpublish') }}</button>
  </OaBulkBar>
  </template>

  <OaPanel
    v-if="panelOpen"
    ref="panel"
    :key="panelKey"
    :title="creating ? t('addAnnouncement') : editing!.title"
    :confirm-label="creating ? t('add') : t('save')"
    :destructive-label="editing ? t('deleteLabel') : undefined"
    :destructive-confirm="editing ? t('confirmDeleteAnnouncement', { name: editing.title }) : undefined"
    :width="480"
    :busy="busy"
    :error="panelError"
    @close="panelClosed"
    @confirm="save"
    @destructive="remove"
  >
    <OaTextField ref="titleField" v-model="form.title" :label="t('announcementTitle')" />
    <OaTextArea
      v-model="form.body"
      :label="t('announcementBody')"
      :rows="8"
      :hint="t('announcementBodyHint')"
    />
    <OaSelectField
      v-model="form.display_mode"
      :label="t('displayMode')"
      :hint="t('displayModeHint')"
      :options="[
        { value: 'always', label: t('displayAlways') },
        { value: 'once', label: t('displayOnce') },
        { value: 'silent', label: t('displaySilent') },
      ]"
    />
    <OaNumberField
      v-model="form.dismiss_after_seconds"
      :label="t('dismissAfter')"
      :min="0"
      :max="60"
      :hint="t('dismissAfterHint')"
    />
    <OaSwitchField v-model="form.published" :label="t('publishedLabel')" :hint="t('publishedHint')" />
    <OaSwitchField v-model="form.pinned" :label="t('pinnedLabel')" />
  </OaPanel>
</template>
