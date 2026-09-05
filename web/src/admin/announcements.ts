// Announcements: what a signed-in user finds in the bell menu.
//
// Draft and pinned are ordinary flags rather than a status enum, because an
// announcement can be either, both or neither, and enumerating the
// combinations would just be these two booleans wearing a costume.

import { ApiError } from '../api/client';
import { t } from '../i18n';
import { button, clear } from '../ui/dom';
import { openPanel, type PanelHandle } from '../ui/panel';
import { numberField, selectField, switchField, textArea, textField } from '../ui/form';
import { badge, badges, relativeTime, renderTable, stacked } from '../ui/table';
import { adminApi, type Announcement } from './api';
import { failure, type AdminView } from './admin-page';

export async function renderAnnouncements(view: AdminView): Promise<void> {
  view.setTitle(t('announcements'));

  let announcements: Announcement[];
  try {
    ({ announcements } = await adminApi.announcements());
  } catch (error) {
    failure(view, error);
    return;
  }

  clear(view.actions);
  view.actions.appendChild(button('oa-btn primary', t('addAnnouncement'), () => {
    editAnnouncement(view, null);
  }));

  clear(view.body);
  view.body.appendChild(renderTable({
    columns: [
      { header: t('colTitle'), cell: (row) => stacked(row.title, bodyPreview(row.body)) },
      { header: t('colAppears'), cell: (row) => badge(displayModeLabel(row.display_mode), 'muted') },
      {
        header: t('colState2'),
        cell: (row) => badges(
          !row.published ? badge(t('draftBadge'), 'danger') : null,
          row.pinned ? badge(t('pinnedBadge'), 'muted') : null,
        ),
      },
      { header: t('colUpdated'), cell: (row) => relativeTime(row.updated_at), secondary: true },
    ],
    rows: announcements,
    empty: t('announcementsEmpty'),
    muted: (row) => !row.published,
    onSelect: (row) => editAnnouncement(view, row),
  }));
}

/** The first ~60 characters of the body, for the table's second line. */
function bodyPreview(body: string): string {
  const collapsed = body.replace(/\s+/g, ' ').trim();
  return collapsed.length > 60 ? `${collapsed.slice(0, 60)}…` : collapsed;
}

function displayModeLabel(mode: Announcement['display_mode']): string {
  switch (mode) {
    case 'always': return t('displayAlways');
    case 'once': return t('displayOnce');
    case 'silent': return t('displaySilent');
  }
}

function editAnnouncement(view: AdminView, existing: Announcement | null): void {
  const creating = existing === null;

  const title = textField({
    label: t('announcementTitle'),
    value: existing?.title ?? '',
  });

  const bodyField = textArea({
    label: t('announcementBody'),
    value: existing?.body ?? '',
    rows: 8,
    hint: t('announcementBodyHint'),
  });

  const displayMode = selectField<Announcement['display_mode']>({
    label: t('displayMode'),
    value: existing?.display_mode ?? 'once',
    hint: t('displayModeHint'),
    options: [
      { value: 'always', label: t('displayAlways') },
      { value: 'once', label: t('displayOnce') },
      { value: 'silent', label: t('displaySilent') },
    ],
  });

  const dismissAfter = numberField({
    label: t('dismissAfter'),
    value: existing?.dismiss_after_seconds ?? 0,
    min: 0,
    max: 60,
    hint: t('dismissAfterHint'),
  });

  const published = switchField({
    label: t('publishedLabel'),
    value: existing?.published ?? false,
    hint: t('publishedHint'),
  });

  const pinned = switchField({
    label: t('pinnedLabel'),
    value: existing?.pinned ?? false,
  });

  openPanel({
    host: view.host,
    title: creating ? t('addAnnouncement') : existing.title,
    confirmLabel: creating ? t('add') : t('save'),
    width: 480,
    ...(existing
      ? {
          destructive: {
            label: t('deleteLabel'),
            confirm: t('confirmDeleteAnnouncement', { name: existing.title }),
            onSelect: (handle) => removeAnnouncement(view, existing, handle),
          },
        }
      : {}),
    build: (body) => {
      body.appendChild(title.element);
      body.appendChild(bodyField.element);
      body.appendChild(displayMode.element);
      body.appendChild(dismissAfter.element);
      body.appendChild(published.element);
      body.appendChild(pinned.element);
    },
    onConfirm: async (handle) => {
      const payload: Record<string, unknown> = {
        title: title.value(),
        body: bodyField.value(),
        display_mode: displayMode.value(),
        dismiss_after_seconds: dismissAfter.value() ?? 0,
        published: published.value(),
        pinned: pinned.value(),
      };

      handle.setBusy(true);
      try {
        if (creating) await adminApi.createAnnouncement(payload);
        else await adminApi.updateAnnouncement(existing.id, payload);
        handle.close();
        view.reload();
      } catch (error) {
        handle.setBusy(false);
        handle.setError(error instanceof ApiError ? error.message : String(error));
      }
    },
  });

  title.focus();
}

async function removeAnnouncement(view: AdminView, announcement: Announcement, panel: PanelHandle): Promise<void> {
  panel.setBusy(true);
  try {
    await adminApi.deleteAnnouncement(announcement.id);
    panel.close();
    view.reload();
  } catch (error) {
    panel.setBusy(false);
    panel.setError(error instanceof ApiError ? error.message : String(error));
  }
}
