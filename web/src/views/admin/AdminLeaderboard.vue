<script setup lang="ts">
// The leaderboard, as the operator decides it.
//
// A page of its own rather than a section under Usage, because the two ask
// different questions: Usage is the operator's own accounting, and this is
// what everybody else is shown about each other. The decisions here are about
// disclosure — whether the board exists for readers at all, and how much of a
// person's identity goes on it — so they are worth their own screen.
//
// Laid out like Availability, which has the same shape: a preview of what
// readers see, then the policy that governs it.

import { computed, onBeforeUnmount, onMounted, ref } from 'vue';
import { adminApi } from '@/admin/api';
import { fetchLeaderboard, type Leaderboard } from '@/api/leaderboard';
import OaNumberField from '@/components/OaNumberField.vue';
import OaSelectField from '@/components/OaSelectField.vue';
import OaSwitchField from '@/components/OaSwitchField.vue';
import { t } from '@/composables/useI18n';
import { IconLock, IconSliders, IconTrophy, IconUsers } from '@/icons';
import { compactNumber } from '@/lib/format';
import AdminControlCard from './AdminControlCard.vue';
import AdminFailure from './AdminFailure.vue';
import AdminWorkbench from './AdminWorkbench.vue';
import { useAdminView } from './adminView';
import { useSettingsDraft } from './settingsDraft';
import type { WorkbenchGroup } from './workbench';
import { errorLine, refusalLine } from './failure-codes';

const view = useAdminView();
view.setTitle(() => t('navLeaderboard'), () => t('leaderboardSubtitle'));

const error = ref('');
const loaded = ref(false);
const busy = ref(false);
const saveLabel = ref('');
// Kept so a newer label is not cleared by an older timer (a second save
// inside the 1.5s showed the button blank while it was still saving), and so
// leaving the page cancels it.
let saveLabelTimer = 0;
const flash = ref('');
const preview = ref<Leaderboard | null>(null);
// A failed read of the preview is not an empty board: the two get different
// lines, or an operator would take a broken endpoint for nobody playing.
const previewFailed = ref(false);

const form = ref({
  showUsers: false,
  identity: 'nickname' as 'nickname' | 'anonymous' | 'handle',
  size: 20 as number | null,
  showModels: true,
});

// A blank box is not a value. It stays blank in the draft, so the page keeps
// saying there are unsaved changes, and save waits for a number (sizeFilled);
// writing the default here would store 20 under a box that still reads empty.
function collect(): Record<string, string> {
  return {
    'leaderboard.show_users': String(form.value.showUsers),
    'leaderboard.identity': form.value.identity,
    'leaderboard.size': form.value.size === null ? '' : String(form.value.size),
    'leaderboard.show_models': String(form.value.showModels),
  };
}

const sizeFilled = computed(() => form.value.size !== null);

const { dirty, changes, accept } = useSettingsDraft(collect);

// What the preview is worth saying about: a board nobody has used yet looks
// broken otherwise, and "there is nothing to rank" is the honest reading.
const participants = computed(() => preview.value?.me.participants ?? 0);

async function save(): Promise<void> {
  if (!loaded.value || error.value || busy.value || !sizeFilled.value) return;
  // Only what this page changed, so the save cannot undo another screen's edit.
  const values = changes();
  if (Object.keys(values).length === 0) return;
  busy.value = true;
  saveLabel.value = t('saving');
  flash.value = '';
  try {
    await adminApi.saveSettings(values);
    accept(values);
    saveLabel.value = t('saved');
    window.clearTimeout(saveLabelTimer);
    saveLabelTimer = window.setTimeout(() => { saveLabel.value = ''; }, 1500);
    try {
      // The preview is the readers' own endpoint, and identity is applied by
      // the server, so it has to be re-asked rather than re-rendered. A refresh
      // that fails leaves the saved settings saved, so it is not reported as a
      // failed save; the old preview simply stays on screen.
      preview.value = await fetchLeaderboard('week', 'tokens');
      previewFailed.value = false;
    } catch {
      // See above: the save already stands.
    }
  } catch (failure) {
    // Only a code the interface has a sentence for is translated; any other
    // keeps the server's sentence, which an administrator may need verbatim.
    flash.value = refusalLine(failure);
    saveLabel.value = '';
  } finally {
    busy.value = false;
  }
}

async function load(): Promise<void> {
  error.value = '';
  try {
    const [settingsData, board] = await Promise.all([
      adminApi.settings(),
      // The same endpoint readers call. An administrator holding this grant
      // may read it before it is published, which is what makes a preview
      // possible at all.
      fetchLeaderboard('week', 'tokens').catch(() => null),
    ]);
    const values = settingsData.settings;
    const identity = values['leaderboard.identity'];
    form.value = {
      showUsers: values['leaderboard.show_users'] === 'true',
      identity: identity === 'anonymous' || identity === 'handle' ? identity : 'nickname',
      size: Number(values['leaderboard.size'] ?? 20),
      showModels: values['leaderboard.show_models'] !== 'false',
    };
    preview.value = board;
    previewFailed.value = board === null;
    accept();
  } catch (failure) {
    error.value = errorLine(failure);
  } finally {
    loaded.value = true;
  }
}

const categories: WorkbenchGroup[] = [
  { id: 'preview', label: 'controlBoardPreview', hint: 'controlBoardPreviewHint', icon: IconTrophy, sections: ['secBoardPreview'] },
  { id: 'privacy', label: 'controlBoardPrivacy', hint: 'controlBoardPrivacyHint', icon: IconLock, sections: ['secBoardVisibility', 'secBoardIdentity'] },
  { id: 'contents', label: 'controlBoardContents', hint: 'controlBoardContentsHint', icon: IconSliders, sections: ['secBoardContents'] },
];

onMounted(load);
onBeforeUnmount(() => window.clearTimeout(saveLabelTimer));
</script>

<template>
  <Teleport :to="view.actionsHost">
    <span v-if="loaded && !error" class="oa-control-save-state" :class="{ dirty }" role="status">
      <span class="oa-dashboard-dot" />{{ dirty ? t('controlUnsaved') : t('controlSaved') }}
    </span>
    <button type="button" class="oa-btn primary" :disabled="busy || !loaded || !!error || !sizeFilled" @click="save">
      {{ saveLabel || t('save') }}
    </button>
  </Teleport>
  <AdminFailure v-if="error" :message="error" @retry="load" />
  <p v-else-if="!loaded" class="oa-table-empty">{{ t('loading') }}</p>
  <AdminWorkbench v-else page="leaderboard" :groups="categories" :searchable="false" v-slot="{ visible }">
    <AdminControlCard
      id="secBoardPreview"
      v-show="visible('secBoardPreview')"
      class="oa-control-card-wide"
      :title="t('secBoardPreview')"
      :hint="t('boardPreviewHint')"
      :icon="IconTrophy"
    >
      <template #actions><div class="oa-control-actions">
        <a href="/leaderboard" target="_blank" rel="noopener" class="oa-btn">{{ t('boardViewPage') }}</a>
      </div></template>
      <p v-if="previewFailed" class="oa-table-empty" role="alert">{{ t('couldNotLoad') }}</p>
      <p v-else-if="!preview || !participants" class="oa-table-empty">{{ t('nothingYet') }}</p>
      <ol v-else class="oa-board-list">
        <li v-for="(entry, index) in preview.accounts" :key="index" class="oa-board-row">
          <span class="oa-board-rank" :data-rank="entry.rank">{{ entry.rank }}</span>
          <span class="oa-board-main">
            <span class="oa-board-name">{{ entry.name || t('boardAnonymous', { rank: entry.rank }) }}</span>
            <span v-if="entry.handle" class="oa-field-hint">@{{ entry.handle }}</span>
          </span>
          <span class="oa-board-value">{{ t('boardTokens', { count: compactNumber(entry.tokens) }) }}</span>
        </li>
      </ol>
    </AdminControlCard>

    <AdminControlCard
      id="secBoardVisibility"
      v-show="visible('secBoardVisibility')"
      :title="t('secBoardVisibility')"
      :icon="IconUsers"
    >
      <OaSwitchField
        v-model="form.showUsers"
        :label="t('boardShowUsers')"
        :hint="t('boardShowUsersHint')"
      />
    </AdminControlCard>

    <AdminControlCard
      id="secBoardIdentity"
      v-show="visible('secBoardIdentity')"
      :title="t('secBoardIdentity')"
      :icon="IconLock"
    >
      <OaSelectField
        v-model="form.identity"
        :label="t('boardIdentity')"
        :hint="t('boardIdentityHint')"
        :searchable="false"
        :options="[
          { value: 'nickname', label: t('boardIdentityNickname') },
          { value: 'anonymous', label: t('boardIdentityAnonymous') },
          { value: 'handle', label: t('boardIdentityHandle') },
        ]"
      />
    </AdminControlCard>

    <AdminControlCard
      id="secBoardContents"
      v-show="visible('secBoardContents')"
      :title="t('secBoardContents')"
      :icon="IconSliders"
    >
      <OaNumberField
        v-model="form.size"
        :label="t('boardSize')"
        :min="1"
        :max="100"
        :hint="t('boardSizeHint')"
      />
      <p v-if="!sizeFilled" class="oa-field-hint" role="alert">{{ t('settingNumberRequired') }}</p>
      <OaSwitchField
        v-model="form.showModels"
        :label="t('boardShowModels')"
        :hint="t('boardShowModelsHint')"
      />
    </AdminControlCard>
  </AdminWorkbench>
  <p v-if="flash" class="oa-drawer-flash visible oa-control-flash" role="alert">{{ flash }}</p>
</template>
