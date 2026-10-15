<script setup lang="ts">
// The bonus bars an account holds, and the switch on the ones it may choose.
//
// A bar is an allowance beside the three windows: spent before them when it is
// on, after them when it is not. Which of those it is right now is what the
// line under each bar says, because the same figure means something different
// in each — credit that is being spent, and credit that is being kept.
//
// The figure beside a bar is worded the way the instance words the allowance
// windows above it: what is left, what has gone, or the figures. The server
// says which with the bars, so it arrives with them rather than a moment after.
// A bar that keeps its amounts to itself is only ever worded as a share.
//
// The panel that hosts this asks it to read again (`refreshKey`) on its own
// timer and after anything that could have changed a bar, a check-in reward,
// say. That read is quiet, like the panel's other refreshes: a failure leaves
// the bars that are on screen, because the last good figures are a better
// answer than an error where they were. The first read, and a switch the server
// refuses, say what went wrong.

import { computed, onMounted, ref, watch } from 'vue';
import { fetchBonus, setBonusChoice, type BonusBar } from '@/api/bonus';
import type { UsageDisplay } from '@/api/usage';
import OaBadge from '@/components/OaBadge.vue';
import OaCollapsible from '@/components/OaCollapsible.vue';
import OaSwitchField from '@/components/OaSwitchField.vue';
import { t, tn } from '@/composables/useI18n';
import { apiFailureText } from '@/lib/api-error-text';
import { absoluteTime } from '@/lib/format';

const props = defineProps<{
  /** Changes whenever the host knows something moved. */
  refreshKey?: number;
}>();

const bars = ref<BonusBar[]>([]);
const display = ref<UsageDisplay>('absolute');
const error = ref('');
/** The bars with a switch in flight. Switching one must not free another. */
const busy = ref<ReadonlySet<string>>(new Set());
/**
 * Bumped by every read; a switch that lands starts one. Reads can overlap — a
 * quiet refresh started before a switch can finish after it, still carrying the
 * bar as it was — so a response only draws if nothing newer has started since.
 */
let generation = 0;

async function load(quiet = false): Promise<void> {
  const request = ++generation;
  try {
    const summary = await fetchBonus();
    if (request !== generation) return;
    bars.value = summary.bars;
    display.value = summary.display ?? 'absolute';
    error.value = '';
  } catch (caught) {
    if (quiet || request !== generation) return;
    error.value = apiFailureText(caught, 'bonusLoadFailed');
  }
}

async function choose(bar: BonusBar, enabled: boolean): Promise<void> {
  const id = bar.bar_id;
  busy.value = new Set([...busy.value, id]);
  try {
    await setBonusChoice(id, enabled);
    // Looked up again: a read that landed while the request was out has
    // replaced the bar objects, and the one the switch was made on is no
    // longer the one on screen.
    const current = bars.value.find((entry) => entry.bar_id === id);
    if (current) current.enabled = enabled;
    error.value = '';
    // A read that was out before the switch carries the bar as it was, and the
    // other figures with it, and that read is dropped now. Reading again brings
    // those figures in; it is quiet, so a failure keeps the switch on screen.
    void load(true);
  } catch (caught) {
    error.value = apiFailureText(caught, 'bonusChoiceFailed', { bonus_not_choosable: 'bonusNotChoosable' });
  } finally {
    const next = new Set(busy.value);
    next.delete(id);
    busy.value = next;
  }
}

function credits(amount: number): string {
  return String(Math.round(amount * 100) / 100);
}

/**
 * The share that is left. A bar with anything in it never reads as empty: the
 * server rounds, and 0.3 of 100 rounds to nothing while requests still succeed.
 */
function left(bar: BonusBar): number {
  return bar.exhausted ? 0 : Math.min(100, Math.max(1, bar.percent));
}

function figure(bar: BonusBar): string {
  if (bar.exhausted) return t('bonusExhausted');
  if (display.value === 'remaining') return t('quotaRemaining', { percent: left(bar) });
  if (display.value === 'used') return t('quotaUsed', { percent: 100 - left(bar) });
  if (bar.show_total && bar.remaining !== null && bar.total !== null) {
    return t('bonusLeft', { left: credits(bar.remaining), total: credits(bar.total) });
  }
  return t('quotaRemaining', { percent: left(bar) });
}

/**
 * The bar has to travel the way the figure beside it reads: an allowance worded
 * as what has gone fills as it is spent, and every other wording drains, which
 * is what "left" looks like.
 */
function fill(bar: BonusBar): string {
  return `${display.value === 'used' ? 100 - left(bar) : left(bar)}%`;
}

/** What is true of the bar right now, in a line. */
function detail(bar: BonusBar): string {
  const parts: string[] = [];
  parts.push(bar.kind === 'reserve' || !bar.enabled ? t('bonusAfterAllowance') : t('bonusBeforeAllowance'));
  parts.push(bar.expires_at > 0 ? t('bonusExpires', { when: absoluteTime(bar.expires_at) }) : t('bonusNoExpiry'));
  if (bar.model_ids.length) parts.push(tn(bar.model_ids.length, 'bonusModelsOne', 'bonusModelsOther'));
  return parts.join(' · ');
}

const visible = computed(() => bars.value);

onMounted(() => load());
watch(() => props.refreshKey, () => load(true));
</script>

<template>
  <OaCollapsible v-if="visible.length || error" id="usage-bonus" :title="t('secBonus')">
    <p v-if="error" class="oa-usage-note error" role="alert">{{ error }}</p>
    <!-- One card with a row per bar, the way the settings screen draws the
         signed-in devices: the allowance's own rows are a shade off the panel,
         and a bar with a switch and a description in it needs more edge than
         that. -->
    <div v-if="visible.length" class="oa-usage-card">
      <div
        v-for="bar in visible"
        :key="bar.bar_id"
        class="oa-usage-card-row oa-bonus-bar"
        :class="{ 'is-spent': bar.exhausted }"
      >
        <div class="oa-usage-row">
          <span class="oa-bonus-name">
            {{ bar.name }}
            <OaBadge v-if="bar.kind === 'reserve'" tone="muted">{{ t('bonusKindReserve') }}</OaBadge>
          </span>
          <span class="oa-usage-value">{{ figure(bar) }}</span>
        </div>
        <div class="oa-meter">
          <div class="oa-meter-fill" :style="{ width: fill(bar) }" />
        </div>
        <div class="oa-usage-reset">{{ detail(bar) }}</div>
        <!-- Operator-written, so text and never markup. -->
        <div v-if="bar.description" class="oa-usage-reset">{{ bar.description }}</div>
        <div v-if="bar.choosable" class="oa-bonus-switch">
          <OaSwitchField
            :model-value="bar.enabled"
            :label="t('bonusUseFirst')"
            :hint="t('bonusUseFirstHint')"
            :disabled="busy.has(bar.bar_id)"
            @update:model-value="choose(bar, $event)"
          />
        </div>
      </div>
    </div>
  </OaCollapsible>
</template>
