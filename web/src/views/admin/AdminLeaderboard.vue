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

import { computed, onMounted, ref } from 'vue';
import { ApiError } from '@/api/client';
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

const view = useAdminView();
view.setTitle(t('navLeaderboard'), t('leaderboardSubtitle'));

const error = ref('');
const loaded = ref(false);
const busy = ref(false);
const saveLabel = ref('');
const flash = ref('');
const preview = ref<Leaderboard | null>(null);

const form = ref({
  showUsers: false,
  identity: 'nickname' as 'nickname' | 'anonymous' | 'handle',
  size: 20 as number | null,
  showModels: true,
});

function collect(): Record<string, string> {
  return {
    'leaderboard.show_users': String(form.value.showUsers),
    'leaderboard.identity': form.value.identity,
    'leaderboard.size': String(form.value.size ?? 20),
    'leaderboard.show_models': String(form.value.showModels),
  };
}

const { dirty, accept } = useSettingsDraft(collect);

// What the preview is worth saying about: a board nobody has used yet looks
// broken otherwise, and "there is nothing to rank" is the honest reading.
const participants = computed(() => preview.value?.me.participants ?? 0);

async function save(): Promise<void> {
  if (!loaded.value || error.value || busy.value) return;
  const values = collect();
  busy.value = true;
  saveLabel.value = t('saving');
  flash.value = '';
  try {
    await adminApi.saveSettings(values);
    accept(values);
    saveLabel.value = t('saved');
    window.setTimeout(() => { saveLabel.value = ''; }, 1500);
    // The preview is the readers' own endpoint, and identity is applied by
    // the server, so it has to be re-asked rather than re-rendered.
    preview.value = await fetchLeaderboard('week', 'tokens');
  } catch (failure) {
    flash.value = failure instanceof ApiError ? failure.message : String(failure);
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
    accept();
  } catch (failure) {
    error.value = failure instanceof Error ? failure.message : String(failure);
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
</script>

<template>
  <Teleport :to="view.actionsHost">
    <span v-if="loaded && !error" class="oa-control-save-state" :class="{ dirty }" role="status">
      <span class="oa-dashboard-dot" />{{ dirty ? t('controlUnsaved') : t('controlSaved') }}
    </span>
    <button type="button" class="oa-btn primary" :disabled="busy || !loaded || !!error" @click="save">
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
      <p v-if="!preview || !participants" class="oa-table-empty">{{ t('nothingYet') }}</p>
      <ol v-else class="oa-board-list">
        <li v-for="entry in preview.accounts" :key="entry.rank" class="oa-board-row">
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
      <OaSwitchField
        v-model="form.showModels"
        :label="t('boardShowModels')"
        :hint="t('boardShowModelsHint')"
      />
    </AdminControlCard>
  </AdminWorkbench>
  <p v-if="flash" class="oa-drawer-flash visible oa-control-flash" role="alert">{{ flash }}</p>
</template>
