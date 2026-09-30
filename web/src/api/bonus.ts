// The bonus bars an account holds something in.
//
// Like the rest of the usage screens this is for display: what a request may
// spend is decided by the server, in the order docs/architecture/
// bonus-and-checkin.md sets down.

import { api } from './client';
import type { UsageDisplay } from './usage';

export interface BonusBar {
  bar_id: string;
  name: string;
  description: string;
  kind: 'bonus' | 'reserve';
  toggle_mode: 'user' | 'on' | 'off';
  /** Whether the account may switch it. */
  choosable: boolean;
  /** Whether it is being spent before the allowance. */
  enabled: boolean;
  show_total: boolean;
  /** Null when the bar keeps its amounts to itself. */
  total: number | null;
  remaining: number | null;
  /** Of what is unexpired: always sent, whatever the bar shows. */
  percent: number;
  /** The soonest expiry among what is left; zero when none of it expires. */
  expires_at: number;
  /** Empty means every model. */
  model_ids: string[];
  exhausted: boolean;
}

export interface BonusSummary {
  bars: BonusBar[];
  /**
   * How the instance words an allowance, sent with the bars for the reason the
   * allowance's own summary sends it: it is only ever read beside the figures it
   * words. Older builds do not send it; the figures are what they always did.
   */
  display?: UsageDisplay;
}

export function fetchBonus(): Promise<BonusSummary> {
  return api.get<BonusSummary>('/api/bonus');
}

export function setBonusChoice(barId: string, enabled: boolean): Promise<void> {
  return api.put<void>(`/api/bonus/${encodeURIComponent(barId)}/choice`, { enabled });
}
