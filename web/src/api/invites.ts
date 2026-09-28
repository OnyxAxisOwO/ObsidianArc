import { api } from './client';

// An account's own invite standing — the personal-referral half of the
// invite system, plus claiming a code somebody else holds (a partner's, or
// another account's, when that code allows it). The admin-issued half
// (batch codes, partner links themselves) lives in the backoffice and is not
// this file's concern.

export interface ProfileInvitee {
  nickname: string;
  username: string;
  created_at: number;
  /** Whether this invite qualified and was added to the inviter's running
   *  total. False either means it has not qualified yet (`reward_skipped`
   *  empty) or it never will (a skip reason below). */
  counted: boolean;
  /** Cards this specific invite triggered — only nonzero on the row that
   *  completed a reward_every milestone. Most counted rows earn none. */
  reward_cards: number;
  /** Why a qualifying-looking invite was still not counted: '', 'same_ip',
   *  'limit', 'disabled', 'inviter_gone' or 'inviter_disabled'. Empty while
   *  pending as much as while counted — `counted` is what disambiguates. */
  reward_skipped: string;
  /** Set when this invitee has processed a group departure. A disabled
   *  invitee rides on its own invite_uses row; a deleted one is the
   *  departure record speaking for an account that no longer exists — the
   *  rest of the fields are still there, but the nickname rides empty. */
  departed?: boolean;
  /** 'disable' or 'delete' — how the departure ended the account. */
  departure_mode?: string;
  /** Cards actually taken back from the inviter — what was due minus what
   *  the inviter had already spent. */
  cards_revoked?: number;
}

export interface ProfileInvites {
  /** Whether accounts get a personal code at all (invites.user_enabled). The
   *  claim box below stays regardless — a code can be claimed with personal
   *  invites off, since claiming and holding one are different things. */
  enabled: boolean;
  /** Empty only when `enabled` is false. */
  code: string;
  /** Successful invites this code may still earn a reward for; 0 = unlimited. */
  limit: number;
  used: number;
  /** Qualifying invites counted toward the every-N reward so far. */
  counted: number;
  /** Every this many counted invites earns a reward; 0 reads as off. */
  reward_every: number;
  /** Cards a milestone grants. 0 = rewards are off, and the progress line
   *  hides itself rather than promising a reward that will not arrive. */
  reward_cards: number;
  reward_card_days: number;
  /** Counted invites still needed before the next milestone; 0 when rewards
   *  are off. */
  next_reward_in: number;
  invitees: ProfileInvitee[];
}

/**
 * Reads the account's invite standing. The server creates the personal code
 * on this same call the first time it is asked for — under the account's own
 * row lock, so two tabs opened at once cannot mint two — which is why there
 * is no separate "create mine" endpoint.
 */
export function fetchProfileInvites(): Promise<ProfileInvites> {
  return api.get<ProfileInvites>('/api/profile/invites');
}

/** Revokes the current personal code and issues a new one in its place. */
export function regenerateProfileInvite(): Promise<ProfileInvites> {
  return api.post<ProfileInvites>('/api/profile/invites/regenerate', {});
}

export interface ClaimResult {
  group_id: string;
  group_name: string;
  /** Days the claim added — what the success message names, alongside the
   *  group, rather than the account's whole remaining balance. */
  days: number;
  expires_at: number;
}

/**
 * Redeems a code this account did not register with — a partner's, or a
 * batch code minted to be claimable after the fact. The server holds the
 * account's row for the whole check-then-write, same as registration; every
 * refusal collapses to a handful of codes an already-signed-in reader can act
 * on (`invite_invalid`, `invite_claimed`, `invite_group_conflict`,
 * `too_many_attempts`).
 */
export function claimInviteCode(code: string): Promise<ClaimResult> {
  return api.post<ClaimResult>('/api/profile/invites/claim', { code });
}

/**
 * How a code reads on screen: the 8-character alphabet the server generates
 * grouped as XXXX-XXXX, same as it is shown printed or read aloud. A custom
 * partner code (any other length) is shown exactly as stored — splitting an
 * operator's own word into four-character chunks would just be harder to read.
 */
export function formatInviteCode(code: string): string {
  return code.length === 8 ? `${code.slice(0, 4)}-${code.slice(4)}` : code;
}

/** Where a personal or partner code sends a new sign-up. */
export function inviteLink(code: string): string {
  return `${window.location.origin}/register?invite=${encodeURIComponent(code)}`;
}
