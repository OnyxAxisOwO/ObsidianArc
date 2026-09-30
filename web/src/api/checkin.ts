// Daily check-in: the account's own page of it. The rules — once a day, what a
// milestone counts — are the server's; this only shows and presses.

import { api } from './client';

export interface CheckinReward {
  kind: '' | 'bonus' | 'card';
  bar_id?: string;
  amount?: number;
  valid_days?: number;
  name?: string;
  windows?: string[];
  cards?: number;
}

export interface CheckinRule {
  id: string;
  title: string;
  basis: 'streak' | 'month';
  days: number;
  reward: CheckinReward;
  progress: number;
  claimable: boolean;
  claimed: boolean;
}

export interface CheckinStatus {
  enabled: boolean;
  /** The day in the administrator's time zone, YYYY-MM-DD. */
  today: string;
  checked_in_today: boolean;
  streak: number;
  /** Days of the month checked in, as day numbers. */
  month_days: number[];
  month_count: number;
  daily: CheckinReward;
  rules: CheckinRule[];
}

export interface CheckinResult {
  day: string;
  streak: number;
  reward: CheckinReward;
  reward_failed: boolean;
}

export function fetchCheckin(): Promise<CheckinStatus> {
  return api.get<CheckinStatus>('/api/checkin');
}

export function checkInNow(): Promise<CheckinResult> {
  return api.post<CheckinResult>('/api/checkin', {});
}

export function claimMilestone(ruleId: string): Promise<{ reward: CheckinReward }> {
  return api.post<{ reward: CheckinReward }>(`/api/checkin/claims/${encodeURIComponent(ruleId)}`, {});
}
