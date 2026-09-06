// The bell in the header and the announcement it sometimes puts in front of
// you.
//
// The server decides which announcement wants attention; this decides whether
// it has already had it during this visit. That split is deliberate: "has
// this reader ever seen it" is durable and belongs in the database, while
// "have we already shown it since the tab was opened" is a property of the
// tab and would be pointless to store.

import { fetchAnnouncements, markAllRead, markRead, type Announcement, type AnnouncementFeed } from '../api/announcements';
import { renderInto } from '../chat/markdown';
import { t } from '../i18n';
import { ICONS, button, el, iconButton } from '../ui/dom';
import { dropdown, menuItem } from '../ui/menu';
import { relativeTime } from '../ui/table';

// Announcements shown during this page load, so an "every visit" one does not
// reappear the moment it is dismissed.
const shownThisVisit = new Set<string>();

export interface Bell {
  /** The trigger plus its menu, ready to append to the header. */
  element: HTMLElement;
  /** Re-reads the feed and repaints the dot. */
  refresh(): void;
}

/**
 * Builds the header bell. Returns immediately with an empty one and fills it
 * in when the feed arrives: the header must not wait on a request that is a
 * courtesy.
 */
export function createBell(): Bell {
  let feed: AnnouncementFeed = { announcements: [], unread: 0, popup: null };

  const trigger = iconButton('oa-icon-btn oa-bell', ICONS.bell, t('announcements'), undefined, 17);
  const dot = el('span', 'oa-bell-dot');
  dot.hidden = true;
  trigger.appendChild(dot);

  const menu = dropdown(trigger, (panel, close) => {
    fill(panel, close);

    // The feed is fetched once when the header is built. A tab left open
    // since before an announcement was written would otherwise say there are
    // none for as long as it stays open — which is exactly when somebody
    // opens the bell to check. Opening it is the moment the answer is wanted,
    // so that is when it is asked for.
    void refresh({ popup: false }).then(() => {
      if (!menu.isOpen) return;
      panel.textContent = '';
      fill(panel, close);
    });
  }, { menuClass: 'oa-menu-announce' });

  function fill(panel: HTMLElement, close: () => void): void {
    const head = el('div', 'oa-menu-head');
    head.appendChild(el('span', 'oa-menu-head-name', t('announcements')));
    if (feed.unread > 0) {
      head.appendChild(button('oa-menu-head-action', t('markAllRead'), () => {
        close();
        void markAllRead().then(() => refresh());
      }));
    }
    panel.appendChild(head);

    if (!feed.announcements.length) {
      panel.appendChild(el('p', 'oa-menu-empty', t('announcementsEmpty')));
      return;
    }

    for (const record of feed.announcements) {
      panel.appendChild(menuItem({
        title: record.title,
        sub: relativeTime(record.updated_at),
        ...(record.read ? {} : { leading: el('span', 'oa-menu-unread') }),
        onSelect: () => {
          close();
          show(record);
        },
      }));
    }
  }

  function paint(): void {
    dot.hidden = feed.unread === 0;
    trigger.title = feed.unread
      ? `${t('announcements')} · ${t('announcementsUnread', { count: feed.unread })}`
      : t('announcements');
    trigger.setAttribute('aria-label', trigger.title);
  }

  /**
   * Re-reads the feed.
   *
   * `popup: false` for the refresh that happens because the reader opened the
   * bell: they are already looking at the list, and throwing a sheet over it
   * at that moment would cover the thing they asked to see.
   */
  function refresh(options: { popup?: boolean } = {}): Promise<void> {
    return fetchAnnouncements()
      .then((next) => {
        feed = next;
        paint();
        if (options.popup === false) return;
        // Only on the first read of a given announcement per visit. An
        // "every visit" one that has just been dismissed must not come
        // straight back when something else refreshes the feed.
        if (next.popup && !shownThisVisit.has(next.popup.id)) show(next.popup);
      })
      .catch(() => {
        // A bell nobody can ring is not worth an error state.
      });
  }

  void refresh();
  return { element: menu.group, refresh: () => void refresh() };

  function show(record: Announcement): void {
    shownThisVisit.add(record.id);
    openAnnouncement(record, () => {
      void markRead(record.id).then(() => refresh()).catch(() => refresh());
    });
  }
}

/**
 * The announcement itself, over the page.
 *
 * A sheet rather than a column, because it is the one thing here that is
 * genuinely modal: it is shown unprompted and the reader has to deal with it
 * before carrying on.
 */
export function openAnnouncement(record: Announcement, onDismiss: () => void): void {
  document.querySelector('.oa-announce-overlay')?.remove();

  const overlay = el('div', 'oa-announce-overlay');
  const card = el('div', 'oa-announce');

  card.appendChild(el('h2', 'oa-announce-title', record.title));

  const body = el('div', 'ai-answer oa-announce-body');
  // The same renderer the transcript uses: Markdown to nodes, with no
  // innerHTML anywhere in the path. An announcement is written by an
  // administrator, but it is read by everyone, and "the author was trusted"
  // is not a property worth building a second render path around.
  renderInto(body, record.body);
  card.appendChild(body);

  const foot = el('div', 'oa-announce-foot');
  foot.appendChild(el('span', 'oa-announce-when', relativeTime(record.created_at)));
  foot.appendChild(el('span', 'oa-drawer-foot-spacer'));

  const dismiss = button('oa-btn primary', t('announcementDismiss'), () => close());
  foot.appendChild(dismiss);
  card.appendChild(foot);

  overlay.appendChild(card);
  document.body.appendChild(overlay);
  requestAnimationFrame(() => overlay.classList.add('open'));

  // A delay is there to make someone read the first line the first time.
  // Reopening one from the history is not that, so it is not made to wait
  // again. The countdown is on the button so the wait reads as finite
  // rather than broken; the server caps how long it can be.
  let remaining = record.read
    ? 0
    : Math.max(0, Math.min(60, Math.round(record.dismiss_after_seconds)));
  let ticker = 0;
  if (remaining > 0) {
    dismiss.disabled = true;
    dismiss.textContent = t('announcementDismissIn', { count: remaining });
    ticker = window.setInterval(() => {
      remaining -= 1;
      if (remaining > 0) {
        dismiss.textContent = t('announcementDismissIn', { count: remaining });
        return;
      }
      window.clearInterval(ticker);
      ticker = 0;
      dismiss.disabled = false;
      dismiss.textContent = t('announcementDismiss');
    }, 1000);
  }

  function close(): void {
    if (dismiss.disabled) return;
    if (ticker) window.clearInterval(ticker);
    document.removeEventListener('keydown', onKey);
    overlay.classList.remove('open');
    window.setTimeout(() => overlay.remove(), 200);
    onDismiss();
  }

  function onKey(event: KeyboardEvent): void {
    if (event.key === 'Escape') close();
  }
  document.addEventListener('keydown', onKey);

  // Clicking the backdrop counts as dismissing, but only once the button
  // would have allowed it.
  overlay.addEventListener('click', (event) => {
    if (event.target === overlay) close();
  });

  if (!dismiss.disabled) dismiss.focus();
}
