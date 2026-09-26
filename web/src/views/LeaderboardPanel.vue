<script setup lang="ts">
// Where the reader stands among everybody else.
//
// A column beside the chat, like Usage and Uptime, and deliberately not the
// backoffice's ranking with a different stylesheet. That one answers "where
// does the money go", so it is ordered by credits and names every account; a
// reader's board answers "how does my use compare", so it carries no prices,
// no account ids, and whatever names the operator decided to publish.
//
// The reader's own standing is pinned above the list rather than left to be
// found in it. Being four hundredth is the common case, and a board that only
// shows the top twenty answers the question for twenty people.

import { onMounted, ref, watch } from 'vue';
import { useRouter } from 'vue-router';
import { ApiError } from '@/api/client';
import {
  fetchLeaderboard,
  type Leaderboard,
  type LeaderboardEntry,
  type LeaderboardMetric,
  type LeaderboardPeriod,
} from '@/api/leaderboard';
import OaFormSection from '@/components/OaFormSection.vue';
import OaIconButton from '@/components/OaIconButton.vue';
import OaPanel from '@/components/OaPanel.vue';
import { t } from '@/composables/useI18n';
import { IconCollapse, IconExpand, IconTrophy } from '@/icons';
import { initials, safeAvatar } from '@/lib/account';
import { compactNumber } from '@/lib/format';

const router = useRouter();

const panel = ref<InstanceType<typeof OaPanel> | null>(null);
const fullscreen = ref(false);

const loading = ref(true);
const error = ref('');
const data = ref<Leaderboard | null>(null);

const period = ref<LeaderboardPeriod>('week');
const metric = ref<LeaderboardMetric>('tokens');

const PERIODS: Array<{ value: LeaderboardPeriod; label: 'boardDay' | 'boardWeek' | 'boardMonth' }> = [
  { value: 'day', label: 'boardDay' },
  { value: 'week', label: 'boardWeek' },
  { value: 'month', label: 'boardMonth' },
];

function toggleFullscreen(): void {
  fullscreen.value = panel.value?.toggleFullscreen() ?? false;
}

/**
 * What to call a row.
 *
 * A row with no name is not missing one: the operator asked for an anonymous
 * board, and a place number is the whole identity. The reader's own row is
 * named whatever the mode, which is why this is asked per row.
 */
function nameFor(entry: LeaderboardEntry): string {
  if (entry.name) return entry.name;
  return entry.self ? t('boardYou') : t('boardAnonymous', { rank: entry.rank });
}

/**
 * What goes in the circle where a picture would be.
 *
 * A name's initial, except on an anonymous row, where there is no name to
 * take one from: running the place label through `initials` produced word
 * salad ("第 1 名" → "第名"), which is what a name-shaped helper does to
 * something that is not a name. The place number is the identity there, so
 * it is also the mark.
 */
function avatarLetter(entry: LeaderboardEntry): string {
  return entry.name ? initials(entry.name) : String(entry.rank);
}

function figure(value: number): string {
  return metric.value === 'requests'
    ? t('boardRequests', { count: compactNumber(value) })
    : t('boardTokens', { count: compactNumber(value) });
}

/** The bar's width, as a share of the leader rather than of the total. */
function share(value: number): string {
  const top = data.value?.accounts[0]?.value ?? 0;
  return top > 0 ? `${Math.max(2, (value / top) * 100)}%` : '0%';
}

async function load(): Promise<void> {
  loading.value = true;
  error.value = '';
  try {
    data.value = await fetchLeaderboard(period.value, metric.value);
  } catch (failure) {
    error.value = failure instanceof ApiError ? failure.message : t('failed');
  } finally {
    loading.value = false;
  }
}

// The window and the measure are both server-side aggregates, so changing
// either is a new question rather than a re-sort of the answer on screen.
watch([period, metric], () => void load());

onMounted(load);
</script>

<template>
  <OaPanel
    ref="panel"
    :title="t('leaderboardTitle')"
    :footer="false"
    :width="460"
    :busy="loading"
    :error="error"
    body-class="oa-board-body"
    @close="router.replace('/')"
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

    <div v-if="data" class="oa-board-content">
      <div class="oa-board-controls">
        <div class="oa-segment" role="group" :aria-label="t('boardPeriod')">
          <button
            v-for="entry in PERIODS"
            :key="entry.value"
            type="button"
            :aria-pressed="period === entry.value"
            @click="period = entry.value"
          >{{ t(entry.label) }}</button>
        </div>
        <div class="oa-segment" role="group" :aria-label="t('rankMetric')">
          <button type="button" :aria-pressed="metric === 'tokens'" @click="metric = 'tokens'">
            {{ t('statTokens') }}
          </button>
          <button type="button" :aria-pressed="metric === 'requests'" @click="metric = 'requests'">
            {{ t('statRequests') }}
          </button>
        </div>
      </div>

      <!-- Pinned, because the reader's place is the one fact this screen
           exists to tell them and it is usually not on the visible list. -->
      <div class="oa-board-me">
        <div class="oa-board-me-rank">
          <template v-if="data.me.rank">
            <span class="oa-board-me-hash">#</span>{{ data.me.rank }}
          </template>
          <IconTrophy v-else :size="20" />
        </div>
        <div class="oa-board-me-text">
          <span class="oa-board-me-title">
            {{ data.me.rank
              ? t('boardYourRank', { total: data.me.participants })
              : t('boardNotRanked') }}
          </span>
          <span class="oa-field-hint">
            {{ data.me.rank ? figure(data.me.value) : t('boardNotRankedHint') }}
            <template v-if="data.me.gap > 0">
              · {{ t('boardBehind', { amount: compactNumber(data.me.gap) }) }}
            </template>
          </span>
        </div>
      </div>

      <OaFormSection :title="t('boardAccounts')" />
      <p v-if="!data.accounts.length" class="oa-menu-empty">{{ t('nothingYet') }}</p>
      <ol v-else class="oa-board-list">
        <li
          v-for="entry in data.accounts"
          :key="entry.rank + (entry.handle ?? entry.name ?? '')"
          class="oa-board-row"
          :class="{ self: entry.self, podium: entry.rank <= 3 }"
        >
          <span class="oa-board-rank" :data-rank="entry.rank">{{ entry.rank }}</span>
          <span class="oa-avatar oa-board-avatar" aria-hidden="true">
            <img
              v-if="entry.avatar && safeAvatar(entry.avatar)"
              :src="safeAvatar(entry.avatar) ?? ''"
              alt=""
              :draggable="false"
            >
            <template v-else>{{ avatarLetter(entry) }}</template>
          </span>
          <span class="oa-board-main">
            <span class="oa-board-name">
              {{ nameFor(entry) }}
              <span v-if="entry.self" class="oa-badge">{{ t('boardYou') }}</span>
            </span>
            <span v-if="entry.handle" class="oa-field-hint">@{{ entry.handle }}</span>
            <span class="oa-board-track" aria-hidden="true">
              <span :style="{ width: share(entry.value) }" />
            </span>
          </span>
          <span class="oa-board-value">{{ compactNumber(entry.value) }}</span>
        </li>
      </ol>

      <template v-if="data.models?.length">
        <OaFormSection :title="t('boardModels')" :hint="t('boardModelsHint')" />
        <ol class="oa-board-list">
          <li v-for="entry in data.models" :key="entry.name" class="oa-board-row">
            <span class="oa-board-rank" :data-rank="entry.rank">{{ entry.rank }}</span>
            <span class="oa-board-main">
              <span class="oa-board-name">{{ entry.name }}</span>
              <span class="oa-field-hint">{{ figure(entry.value) }}</span>
            </span>
            <span class="oa-board-value">{{ t('boardUsersCount', { count: entry.users }) }}</span>
          </li>
        </ol>
      </template>
    </div>
  </OaPanel>
</template>
