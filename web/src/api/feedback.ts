// Feedback, as the person who sent it sees it: their reports, the
// conversation on each one, and their own turn in it.
//
// The enums are the server's own values, English on purpose like every other
// protocol string in this project: the screen translates them at render, and
// what travels is what the API documents.
//
// Bodies are Markdown on both sides and are handed around as the text they
// were typed as. Nothing here renders anything — `OaMarkdown` does, through
// the transcript's own node-building renderer.

import { api } from './client';

export type FeedbackKind = 'bug' | 'idea';
export type FeedbackPriority = 'low' | 'medium' | 'high';
export type FeedbackStatus = 'open' | 'resolved';

export interface Feedback {
  id: string;
  user_id: string;
  kind: FeedbackKind;
  priority: FeedbackPriority;
  status: FeedbackStatus;
  title: string;
  body: string;
  created_at: number;
  updated_at: number;
  /** How many turns the conversation has, the report itself aside. */
  replies: number;
  /** Whether this side has seen the other's last word. */
  author_unread: boolean;
  operator_unread: boolean;
  /**
   * Who wrote it. Filled only in the operator's listing — an author reading
   * their own knows who they are, and the join is not free.
   */
  username?: string;
  nickname?: string;
}

/** One turn in a thread. `from_staff` is which side said it. */
export interface FeedbackReply {
  id: string;
  feedback_id: string;
  user_id: string;
  from_staff: boolean;
  body: string;
  created_at: number;
  username?: string;
  nickname?: string;
}

export interface FeedbackThread {
  feedback: Feedback;
  replies: FeedbackReply[];
}

interface FeedbackList {
  feedback: Feedback[];
  /** How many more this account may send today. */
  remaining: number;
  max_per_day: number;
}

export function listFeedback(): Promise<FeedbackList> {
  return api.get<FeedbackList>('/api/feedback');
}

export function sendFeedback(body: {
  kind: FeedbackKind;
  priority: FeedbackPriority;
  title: string;
  body: string;
  /** The challenge token, where the operator has switched one on. */
  turnstile?: string;
}): Promise<Feedback> {
  return api.post<Feedback>('/api/feedback', body);
}

/**
 * One of the caller's own threads.
 *
 * Fetching it is also what marks the answer read — there is no other reason
 * to open a thread, so the server does not make the client say so twice.
 */
export function fetchThread(id: string): Promise<FeedbackThread> {
  return api.get<FeedbackThread>(`/api/feedback/${id}`);
}

/**
 * The author's own turn on their own thread.
 *
 * Carries a challenge token where the operator has switched the check on for
 * feedback: a thread holds fifty turns against ten reports a day, so this is
 * the cheaper of the two writes to automate and is gated the same way.
 */
export function replyToFeedback(
  id: string, body: string, turnstile = '',
): Promise<FeedbackReply> {
  return api.post<FeedbackReply>(`/api/feedback/${id}/replies`, {
    body,
    ...(turnstile ? { turnstile } : {}),
  });
}

/** How many of this account's reports have an answer it has not opened. */
export function fetchFeedbackUnread(): Promise<{ unread: number }> {
  return api.get<{ unread: number }>('/api/feedback/unread');
}
