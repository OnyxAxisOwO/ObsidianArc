// What the operator has told everyone, and whether this reader has seen it.

import { api } from './client';

export type DisplayMode = 'always' | 'once' | 'silent';

export interface Announcement {
  id: string;
  title: string;
  /** Markdown. Rendered through the transcript's renderer, never as HTML. */
  body: string;
  display_mode: DisplayMode;
  dismiss_after_seconds: number;
  published: boolean;
  pinned: boolean;
  created_at: number;
  updated_at: number;
  read: boolean;
}

export interface AnnouncementFeed {
  announcements: Announcement[];
  unread: number;
  /**
   * The one to put in front of the reader now, decided by the server so the
   * rule lives in one place. Null when nothing is asking for attention.
   */
  popup: Announcement | null;
}

export function fetchAnnouncements(): Promise<AnnouncementFeed> {
  return api.get<AnnouncementFeed>('/api/announcements');
}

export function markRead(id: string): Promise<void> {
  return api.post<void>(`/api/announcements/${encodeURIComponent(id)}/read`, {});
}

export function markAllRead(): Promise<void> {
  return api.post<void>('/api/announcements/read', {});
}
