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
import OaBadge from '@/components/OaBadge.vue';
import OaCollapsible from '@/components/OaCollapsible.vue';
import { t } from '@/composables/useI18n';

const emit = defineEmits<{ changed: [] }>();

const section = ref<InstanceType<typeof OaCollapsible> | null>(null);
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
    section.value?.reveal();
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
    section.value?.reveal();
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
/** Something is waiting to be claimed: said in the heading, for a section that is folded. */
const claimable = computed(() => !!status.value?.rules.some((rule) => rule.claimable));

onMounted(load);
</script>

<template>
  <OaCollapsible v-if="status?.enabled" id="usage-checkin" ref="section" :title="t('secCheckin')">
    <!-- The streak, what is waiting and the day's button are in the heading, so a
         reader who keeps the section folded can still check in and see that
         there is something to claim. -->
    <template #meta>
      <span v-if="status.streak" class="oa-card-total">{{ t('checkinStreak', { count: status.streak }) }}</span>
      <OaBadge v-if="claimable">{{ t('checkinClaimable') }}</OaBadge>
    </template>
    <template #actions>
      <button
        type="button"
        class="oa-btn"
        :class="{ primary: !status.checked_in_today }"
        :disabled="status.checked_in_today || busy === 'now'"
        @click="checkIn"
      >{{ status.checked_in_today ? t('checkinDone') : t('checkinNow') }}</button>
    </template>

    <!-- One card, the way the settings screen draws two-step verification: what
         a check-in earns, the month, and a row for each milestone, divided by
         hairlines. -->
    <div class="oa-usage-card">
      <div v-if="dailyText || notice || error" class="oa-usage-card-row">
        <p v-if="notice" class="oa-usage-note" role="status">{{ notice }}</p>
        <p v-if="error" class="oa-usage-note error" role="alert">{{ error }}</p>
        <p v-if="dailyText" class="oa-checkin-meta">{{ t('checkinDaily', { reward: dailyText }) }}</p>
      </div>

      <div class="oa-usage-card-row">
        <div class="oa-checkin-days" :aria-label="t('checkinMonth', { count: status.month_count })">
          <span
            v-for="day in monthDays"
            :key="day"
            class="oa-checkin-day"
            :class="{ on: checked.has(day), today: day === todayNumber }"
          >{{ day }}</span>
        </div>
        <p class="oa-checkin-count">{{ t('checkinMonth', { count: status.month_count }) }}</p>
      </div>

      <div v-for="rule in status.rules" :key="rule.id" class="oa-usage-card-row oa-checkin-milestone">
        <div class="oa-checkin-milestone-text">
          <span class="oa-checkin-milestone-title">{{ ruleTitle(rule) }}</span>
          <span class="oa-checkin-milestone-meta">{{ rule.progress }} / {{ rule.days }} · {{ rewardText(rule.reward) }}</span>
        </div>
        <button
          type="button"
          class="oa-btn"
          :class="{ primary: rule.claimable }"
          :disabled="!rule.claimable || busy === rule.id"
          @click="claim(rule)"
        >{{ rule.claimed ? t('checkinClaimed') : t('checkinClaim') }}</button>
      </div>
    </div>
  </OaCollapsible>
</template>
