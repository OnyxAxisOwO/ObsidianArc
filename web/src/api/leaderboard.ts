// The leaderboard, as a reader is allowed to see it.
//
// Narrow on purpose, matching the server: no account ids, no credits, no
// providers. What the page cannot be handed, it cannot show by mistake.

import { api } from './client';

export type LeaderboardPeriod = 'day' | 'week' | 'month';
export type LeaderboardMetric = 'tokens' | 'requests';
export type LeaderboardIdentity = 'nickname' | 'anonymous' | 'handle';

export interface LeaderboardEntry {
  rank: number;
  /** Absent for everyone but the reader when the board is anonymous. */
  name?: string;
  handle?: string;
  avatar?: string;
  value: number;
  requests: number;
  tokens: number;
  models: number;
  self?: boolean;
}

export interface LeaderboardModel {
  rank: number;
  name: string;
  avatar?: string;
  value: number;
  users: number;
  requests: number;
  tokens: number;
}

export interface LeaderboardStanding {
  /** Zero when the reader has no answered turns in the window. */
  rank: number;
  value: number;
  gap: number;
  participants: number;
}

export interface Leaderboard {
  period: LeaderboardPeriod;
  metric: LeaderboardMetric;
  identity: LeaderboardIdentity;
  accounts: LeaderboardEntry[];
  me: LeaderboardStanding;
  /** Absent when the operator has switched the models board off. */
  models?: LeaderboardModel[];
}

export function fetchLeaderboard(period: LeaderboardPeriod, metric: LeaderboardMetric): Promise<Leaderboard> {
  return api.get<Leaderboard>(`/api/leaderboard?period=${period}&metric=${metric}`);
}
