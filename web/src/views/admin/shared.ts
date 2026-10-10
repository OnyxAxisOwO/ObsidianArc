// The pieces more than one administration screen needs.

import { adminApi, type AdminModel } from '@/admin/api';
import { t } from '@/composables/useI18n';
import { onSignOut } from '@/stores/session';

/**
 * The models an allowance is actually spent on. Fetched once per visit and
 * shared by every policy form, because three screens ask the same question
 * and none of them is worth a request of its own.
 */
let pricing: Promise<AdminModel[]> | null = null;
// The list belongs to the session that fetched it, so the next administrator
// asks again rather than reading the last one's copy.
onSignOut(() => { pricing = null; });

export function pricedModels(): Promise<AdminModel[]> {
  if (!pricing) {
    pricing = adminApi.models()
      .then(({ models }) => models.filter((entry) => entry.enabled))
      // A failed lookup costs the note, not the form.
      .catch((): AdminModel[] => []);
  }
  return pricing;
}

/**
 * What one turn reserves before it runs, mirroring Model.WorstCase in Go.
 *
 * The reservation, not the average cost: it is what the limit is actually
 * compared against, so it is what decides whether an allowance can pay for
 * anything at all. A ceiling under one turn's reservation refuses the first
 * message rather than running out partway through the day, and that is worth
 * being told before saving rather than after a user complains.
 */
export function worstCase(entry: AdminModel): number {
  const ceiling = entry.max_output_tokens > 0 && entry.max_output_tokens < 4096
    ? entry.max_output_tokens
    : 4096;
  return entry.request_weight + (ceiling / 1000) * entry.output_token_weight;
}

/** Priced against the most expensive model, because that is the one that
 *  decides when somebody is cut off. */
export function priciest(models: AdminModel[]): AdminModel | null {
  return models.reduce<AdminModel | null>(
    (worst, entry) => (worst === null || worstCase(entry) > worstCase(worst) ? entry : worst),
    null,
  );
}

/** The few models that cost the most per turn, dearest first: the readout
 *  names several because the dearest one alone says nothing about the rest. */
export function priciestFew(models: AdminModel[], count: number): AdminModel[] {
  return [...models].sort((a, b) => worstCase(b) - worstCase(a)).slice(0, count);
}

export function round(value: number): string {
  return String(Math.round(value * 10) / 10);
}

/** Minutes in the largest unit that divides them evenly, so 1440 reads as a
 *  day rather than a number to divide in one's head. */
export function minutesLabel(minutes: number): string {
  if (minutes > 0 && minutes % 1440 === 0) return t('durationDays', { count: minutes / 1440 });
  if (minutes > 0 && minutes % 60 === 0) return t('durationHours', { count: minutes / 60 });
  return t('durationMinutes', { count: minutes });
}
