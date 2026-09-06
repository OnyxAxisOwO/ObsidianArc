// Redemption codes: a batch of usage resets behind a string somebody types.
//
// The cards themselves are not listed here. A code is the thing an operator
// creates and hands out; where its cards ended up is a question about
// accounts, and it is answered in the account drawer.

import { ApiError } from '../api/client';
import { copyToClipboard } from '../chat/markdown';
import { t } from '../i18n';
import { button, clear, el } from '../ui/dom';
import { openPanel, type PanelHandle } from '../ui/panel';
import { numberField, textField } from '../ui/form';
import { badge, badges, relativeTime, renderTable } from '../ui/table';
import { adminApi, type RedemptionCode } from './api';
import { failure, type AdminView } from './admin-page';

export async function renderCodes(view: AdminView): Promise<void> {
  view.setTitle(t('codesTitle'), t('codesSubtitle'));

  let codes: RedemptionCode[];
  try {
    ({ codes } = await adminApi.codes());
  } catch (error) {
    failure(view, error);
    return;
  }

  clear(view.actions);
  view.actions.appendChild(button('oa-btn primary', t('addCode'), () => editCode(view)));

  clear(view.body);
  view.body.appendChild(renderTable({
    columns: [
      {
        header: t('colCode'),
        cell: (row) => {
          const wrap = el('div', 'oa-cell-stack');
          const code = el('span', 'oa-cell-title', row.code);
          code.style.fontFamily = 'ui-monospace, SFMono-Regular, Menlo, Consolas, monospace';
          wrap.appendChild(code);
          if (row.note) wrap.appendChild(el('span', 'oa-cell-sub', row.note));
          return wrap;
        },
      },
      {
        header: t('colClaimed'),
        // Both halves, because "12" answers nothing without the ceiling it is
        // approaching.
        cell: (row) => `${row.claimed} / ${row.cards}`,
        numeric: true,
        width: '100px',
        sort: (row) => row.claimed / Math.max(1, row.cards),
      },
      {
        header: t('colCardLife'),
        cell: (row) => t('nDays', { count: row.card_days }),
        secondary: true,
        width: '90px',
      },
      {
        header: t('colState'),
        cell: (row) => badges(
          row.claimed >= row.cards ? badge(t('codeEmptied'), 'muted') : null,
          row.expires_at > 0 && row.expires_at <= Date.now() ? badge(t('codeExpired'), 'danger') : null,
        ),
        width: '110px',
      },
      {
        header: t('colUpdated'),
        cell: (row) => relativeTime(row.created_at),
        secondary: true,
        width: '110px',
      },
    ],
    rows: codes,
    empty: t('noCodes'),
    muted: (row) => row.claimed >= row.cards,
    onSelect: (row) => editCode(view, row),
  }));
}

function editCode(view: AdminView, existing?: RedemptionCode): void {
  // An existing code is shown rather than edited. Changing how many cards a
  // code carries after people have redeemed it is a decision with no honest
  // answer — the ones already handed out do not come back — so the only
  // action offered is withdrawing it.
  const creating = !existing;

  const count = numberField({
    label: t('codeCount'),
    value: 1,
    min: 1,
    max: 200,
    hint: t('codeCountHint'),
    onInput: () => panel.rebuild(),
  });
  const code = textField({
    label: t('colCode'),
    value: existing?.code ?? '',
    placeholder: 'WELCOME2026',
    hint: t('codeHint'),
    monospace: true,
  });
  const cards = numberField({
    label: t('codeCards'),
    value: existing?.cards ?? 10,
    min: 1,
    hint: t('codeCardsHint'),
  });
  const cardDays = numberField({
    label: t('codeCardDays'),
    value: existing?.card_days ?? 30,
    min: 1,
    hint: t('codeCardDaysHint'),
  });
  const expiresDays = numberField({
    label: t('codeExpiresDays'),
    value: null,
    min: 0,
    placeholder: t('noLimit'),
    hint: t('codeExpiresHint'),
  });

  const panel = openPanel({
    host: view.host,
    title: creating ? t('addCode') : existing.code,
    ...(creating ? { confirmLabel: t('add') } : { footer: false }),
    ...(existing
      ? {
          destructive: {
            label: t('deleteLabel'),
            confirm: t('confirmDeleteCode', { code: existing.code }),
            onSelect: (handle) => removeCode(view, existing, handle),
          },
        }
      : {}),
    build: (body) => {
      if (!creating) {
        body.appendChild(el('p', 'oa-field-hint',
          t('codeClaimedSoFar', { claimed: existing.claimed, cards: existing.cards })));
        return;
      }
      body.appendChild(count.element);
      // A batch is generated, so there is nothing to name. Hiding the field
      // rather than disabling it, because a disabled box still looks like
      // somewhere to type.
      if ((count.value() ?? 1) <= 1) body.appendChild(code.element);
      body.appendChild(cards.element);
      body.appendChild(cardDays.element);
      body.appendChild(expiresDays.element);
    },
    ...(creating
      ? {
          onConfirm: async (handle) => {
            const days = expiresDays.value();
            const batch = count.value() ?? 1;
            handle.setBusy(true);
            try {
              const { codes: minted } = await adminApi.createCode({
                code: batch > 1 ? '' : code.value(),
                count: batch,
                cards: cards.value() ?? 1,
                card_days: cardDays.value() ?? 30,
                // Zero is "never", which is what an empty field means here.
                expires_at: days && days > 0 ? Date.now() + days * 24 * 3600 * 1000 : 0,
              });
              handle.setBusy(false);
              // Generated codes exist nowhere else until they are copied off
              // this screen, so the panel turns into the list rather than
              // closing over them.
              showMinted(handle, minted);
              view.reload();
            } catch (error) {
              handle.setBusy(false);
              handle.setError(error instanceof ApiError ? error.message : String(error));
            }
          },
        }
      : {}),
  });
}

/** The batch, once, with a way to take it away in one piece. */
function showMinted(handle: PanelHandle, minted: RedemptionCode[]): void {
  handle.setTitle(t('codesMinted', { count: minted.length }));
  const lines = minted.map((entry) => entry.code).join(String.fromCharCode(10));

  handle.body.textContent = '';
  handle.body.appendChild(el('p', 'oa-field-hint', t('codesMintedHint')));

  const copy = button('oa-btn primary', t('copyAll'), () => {
    void copyToClipboard(lines).then((ok) => {
      if (ok) copy.textContent = t('copied');
    });
  });
  handle.body.appendChild(copy);

  const list = el('div', 'oa-code-list');
  for (const entry of minted) {
    list.appendChild(el('code', 'oa-code-line', entry.code));
  }
  handle.body.appendChild(list);
}

async function removeCode(view: AdminView, code: RedemptionCode, panel: PanelHandle): Promise<void> {
  panel.setBusy(true);
  try {
    await adminApi.deleteCode(code.id);
    panel.close();
    view.reload();
  } catch (error) {
    panel.setBusy(false);
    panel.setError(error instanceof ApiError ? error.message : String(error));
  }
}
