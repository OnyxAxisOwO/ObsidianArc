// The pieces more than one administration screen needs.

import { adminApi, type AdminModel } from '@/admin/api';

/**
 * The models an allowance is actually spent on. Fetched once per visit and
 * shared by every policy form, because three screens ask the same question
 * and none of them is worth a request of its own.
 */
let pricing: Promise<AdminModel[]> | null = null;

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
  const ceiling = entry.max_output_tokens > 0 ? entry.max_output_tokens : 4096;
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

export function round(value: number): string {
  return String(Math.round(value * 10) / 10);
}
