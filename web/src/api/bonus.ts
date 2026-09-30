// The bonus bars an account holds something in.
//
// Like the rest of the usage screens this is for display: what a request may
// spend is decided by the server, in the order docs/architecture/
// bonus-and-checkin.md sets down.

import { api } from './client';

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

export function fetchBonus(): Promise<{ bars: BonusBar[] }> {
  return api.get<{ bars: BonusBar[] }>('/api/bonus');
}

export function setBonusChoice(barId: string, enabled: boolean): Promise<void> {
  return api.put<void>(`/api/bonus/${encodeURIComponent(barId)}/choice`, { enabled });
}
