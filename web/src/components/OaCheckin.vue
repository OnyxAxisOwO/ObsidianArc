<script setup lang="ts">
// The daily check-in: a button, the month so far, and the milestones.
//
// Nothing here decides anything. Whether it is today's first check-in, what a
// milestone counts, whether the reward can be claimed — the server answers
// each, and the buttons are drawn from what it says. Draws nothing at all when
// the administrator has not switched check-in on.

import { computed, onMounted, ref } from 'vue';
import { ApiError } from '@/api/client';
import {
  checkInNow, claimMilestone, fetchCheckin,
  type CheckinReward, type CheckinRule, type CheckinStatus,
} from '@/api/checkin';
import { t } from '@/composables/useI18n';

const emit = defineEmits<{ changed: [] }>();

const status = ref<CheckinStatus | null>(null);
const error = ref('');
const notice = ref('');
const busy = ref('');

async function load(): Promise<void> {
  try {
    status.value = await fetchCheckin();
    error.value = '';
  } catch (caught) {
    error.value = caught instanceof ApiError ? caught.message : t('checkinLoadFailed');
  }
}

function rewardText(reward: CheckinReward): string {
  if (reward.kind === 'bonus') {
    const amount = String(Math.round((reward.amount ?? 0) * 100) / 100);
    return reward.valid_days
      ? t('checkinRewardBonus', { amount, days: reward.valid_days })
      : t('checkinRewardBonusForever', { amount });
  }
  if (reward.kind === 'card') {
    return t('checkinRewardCard', { count: reward.cards ?? 1, days: reward.valid_days ?? 0 });
  }
  return '';
}

async function checkIn(): Promise<void> {
  busy.value = 'now';
  notice.value = '';
  try {
    const result = await checkInNow();
    notice.value = result.reward_failed
      ? t('checkinRewardFailed')
      : t('checkinGot', { streak: result.streak, reward: rewardText(result.reward) });
    error.value = '';
    await load();
    emit('changed');
  } catch (caught) {
    // Read first and say why after: the answer to a refusal is usually that
    // the page is a day or a click behind, and a successful read clears the
    // error, which would erase the reason the account is looking for.
    await load();
    error.value = caught instanceof ApiError ? caught.message : t('checkinFailed');
  } finally {
    busy.value = '';
  }
}

async function claim(rule: CheckinRule): Promise<void> {
  busy.value = rule.id;
  notice.value = '';
  try {
    const result = await claimMilestone(rule.id);
    notice.value = t('checkinClaimedNotice', { reward: rewardText(result.reward) });
    error.value = '';
    await load();
    emit('changed');
  } catch (caught) {
    await load();
    error.value = caught instanceof ApiError ? caught.message : t('checkinFailed');
  } finally {
    busy.value = '';
  }
}

function ruleTitle(rule: CheckinRule): string {
  if (rule.title) return rule.title;
  return rule.basis === 'streak'
    ? t('checkinRuleStreak', { days: rule.days })
    : t('checkinRuleMonth', { days: rule.days });
}

/** The month the server's today is in, as day numbers to draw. */
const monthDays = computed(() => {
  const today = status.value?.today;
  if (!today) return [];
  const [year, month] = today.split('-').map(Number) as [number, number];
  const count = new Date(year, month, 0).getDate();
  return Array.from({ length: count }, (_, i) => i + 1);
});
const todayNumber = computed(() => Number(status.value?.today.slice(8) ?? 0));
const checked = computed(() => new Set(status.value?.month_days ?? []));
const dailyText = computed(() => rewardText(status.value?.daily ?? { kind: '' }));

onMounted(load);
</script>

<template>
  <div v-if="status?.enabled" class="oa-checkin">
    <div class="oa-card-head">
      <h3 class="oa-drawer-subhead">{{ t('secCheckin') }}</h3>
      <span v-if="status.streak" class="oa-card-total">{{ t('checkinStreak', { count: status.streak }) }}</span>
      <span class="oa-header-spacer" />
      <button
        type="button"
        class="oa-btn"
        :class="{ primary: !status.checked_in_today }"
        :disabled="status.checked_in_today || busy === 'now'"
        @click="checkIn"
      >{{ status.checked_in_today ? t('checkinDone') : t('checkinNow') }}</button>
    </div>
    <p v-if="dailyText" class="oa-field-hint">{{ t('checkinDaily', { reward: dailyText }) }}</p>
    <p v-if="notice" class="oa-field-hint" role="status">{{ notice }}</p>
    <p v-if="error" class="oa-field-hint">{{ error }}</p>

    <div class="oa-checkin-days" :aria-label="t('checkinMonth', { count: status.month_count })">
      <span
        v-for="day in monthDays"
        :key="day"
        class="oa-checkin-day"
        :class="{ on: checked.has(day), today: day === todayNumber }"
      >{{ day }}</span>
    </div>
    <p class="oa-field-hint">{{ t('checkinMonth', { count: status.month_count }) }}</p>

    <div v-if="status.rules.length" class="oa-card-list">
      <div v-for="rule in status.rules" :key="rule.id" class="oa-card-row">
        <div>
          <span class="oa-card-title">{{ ruleTitle(rule) }}</span>
          <span class="oa-card-sub">{{ rule.progress }} / {{ rule.days }} · {{ rewardText(rule.reward) }}</span>
        </div>
        <span class="oa-header-spacer" />
        <button
          type="button"
          class="oa-btn"
          :disabled="!rule.claimable || busy === rule.id"
          @click="claim(rule)"
        >{{ rule.claimed ? t('checkinClaimed') : t('checkinClaim') }}</button>
      </div>
    </div>
  </div>
</template>
