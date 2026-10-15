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

import { onBeforeUnmount, onMounted, ref, watch } from 'vue';
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
import { usePanelExit } from '@/composables/usePanelExit';
import { IconCollapse, IconExpand, IconTrophy } from '@/icons';
import { initials, safeAvatar } from '@/lib/account';
import { compactNumber } from '@/lib/format';

const panels = usePanelExit();

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
  // The selector moves before the next board arrives, and the rows still on
  // screen are the previous measure's: the unit must come from the board.
  const measure = data.value?.metric ?? metric.value;
  return measure === 'requests'
    ? t('boardRequests', { count: compactNumber(value) })
    : t('boardTokens', { count: compactNumber(value) });
}

/** The bar's width, as a share of the leader rather than of the total. */
function share(value: number): string {
  const top = data.value?.accounts[0]?.value ?? 0;
  return top > 0 ? `${Math.max(2, (value / top) * 100)}%` : '0%';
}

function isAbortError(err: unknown): boolean {
  return (
    (err instanceof DOMException && err.name === 'AbortError') ||
    (err instanceof Error && err.name === 'AbortError') ||
    (typeof err === 'object' && err !== null && (err as { name?: string }).name === 'AbortError')
  );
}

// The rows are shown in the order the server ranked them. The server ranks and
// cuts each measure on its own, so a client re-sort would place the top of one
// measure in the order of another, and the reader's place would not be the
// server's.
function applyMetric(board: Leaderboard, currentMetric: LeaderboardMetric): void {
  board.metric = currentMetric;

  if (board.me && board.accounts && Array.isArray(board.accounts)) {
    const selfEntry = board.accounts.find((a) => a.self);
    if (selfEntry) {
      board.me.rank = selfEntry.rank;
      board.me.value = selfEntry.value;
      let gap = 0;
      const selfIdx = board.accounts.indexOf(selfEntry);
      for (let j = selfIdx - 1; j >= 0; j--) {
        const higher = board.accounts[j];
        if (higher && higher.value > selfEntry.value) {
          gap = higher.value - selfEntry.value;
          break;
        }
      }
      board.me.gap = gap;
    }
  }
}

let abortController: AbortController | null = null;

async function load(): Promise<void> {
  if (abortController) {
    abortController.abort();
    abortController = null;
  }
  const controller = new AbortController();
  abortController = controller;

  loading.value = true;
  error.value = '';
  try {
    const result = await fetchLeaderboard(period.value, metric.value, controller.signal);
    if (controller.signal.aborted) return;
    result.period = period.value;
    applyMetric(result, metric.value);
    data.value = result;
  } catch (failure) {
    if (controller.signal.aborted || isAbortError(failure)) return;
    error.value = failure instanceof ApiError ? failure.message : t('failed');
  } finally {
    if (abortController === controller) {
      abortController = null;
      loading.value = false;
    }
  }
}

// Both the window and the measure are part of the request. The server ranks
// and truncates the board per measure, so a list re-sorted on the client holds
// only the top of the measure it was fetched for, and cannot place the reader
// or the other leaders in the one now shown.
watch(period, () => void load());
watch(metric, () => void load());

onMounted(load);

onBeforeUnmount(() => {
  if (abortController) {
    abortController.abort();
    abortController = null;
  }
});
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
    @close="panels.close('replace')"
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
          v-for="(entry, index) in data.accounts"
          :key="index"
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
