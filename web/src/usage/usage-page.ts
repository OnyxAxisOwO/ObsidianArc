// What this account has spent, and on what.
//
// A panel over the chat, like Settings and About, because it is the same kind
// of thing: somewhere you go to look at your own account and come back from.
// The composer's menu keeps its own short version of the allowance — that one
// answers "can I send this", which is a question asked mid-sentence and does
// not want a screen.

import { ApiError, api } from '../api/client';
import { ICONS, button, clear, el, iconButton } from '../ui/dom';
import { fetchUsage, type UsageSummary } from '../api/usage';
import { renderChatPage } from '../chat/chat-page';
import { t } from '../i18n';
import { navigate } from '../router';
import { section } from '../ui/form';
import { openPanel } from '../ui/panel';
import { compactNumber, relativeTime, renderTable } from '../ui/table';
import { celebrate } from '../ui/confetti';
import { usageWindow } from '../ui/usage-meter';

interface Totals {
  requests: number;
  input_tokens: number;
  output_tokens: number;
  reasoning_tokens: number;
  total_tokens: number;
  credits: number;
  errors: number;
}

/** One reset somebody was given, and until when they may spend it. */
interface Card {
  id: string;
  source: 'grant' | 'code';
  expires_at: number;
}

/** One turn, as narrow as the server will describe it. */
interface Turn {
  id: string;
  model_name: string;
  input_tokens: number;
  output_tokens: number;
  reasoning_tokens: number;
  total_tokens: number;
  credits: number;
  status: 'ok' | 'error' | 'aborted' | 'rejected';
  error_code?: string;
  started_at: number;
  finished_at: number;
}

export function renderUsagePage(root: HTMLElement): void {
  const host = renderChatPage(root);

  openPanel({
    host,
    title: t('navUsage'),
    footer: false,
    width: 460,
    onClose: () => navigate('/'),
    build: (body) => {
      const allowance = el('div', 'oa-usage-list');
      const stats = el('div', 'oa-stat-grid');
      const history = el('div');

      body.appendChild(section(t('secAllowance')));
      body.appendChild(allowance);
      body.appendChild(cardSection());
      body.appendChild(section(t('secTotals')));
      body.appendChild(stats);
      body.appendChild(section(t('secRecentTurns')));
      body.appendChild(history);

      // Two requests rather than one endpoint returning everything: the
      // allowance is already served, cached and read by the composer every
      // few seconds, and folding a page of history into that hot path would
      // make every menu open carry fifty rows nobody opened it for.
      void fetchUsage()
        .then((summary) => paintAllowance(allowance, summary))
        .catch(() => {
          allowance.appendChild(el('p', 'oa-field-hint', t('usageUnavailable')));
        });

      void api.get<{ totals: Totals; turns: Turn[] }>('/api/usage/me/history')
        .then((payload) => {
          paintTotals(stats, payload.totals);
          history.appendChild(turnTable(payload.turns));
        })
        .catch((error: unknown) => {
          history.appendChild(el('p', 'oa-field-hint',
            error instanceof ApiError ? error.message : t('usageUnavailable')));
        });
    },
  });
}

/**
 * The resets this account is holding, and the way to acquire another.
 *
 * The plus is beside the heading rather than under the list, because it is
 * the thing to reach for when the list is empty — which is when somebody with
 * a code in their hand is looking at this screen.
 */
function cardSection(): HTMLElement {
  const wrap = el('div');

  const head = el('div', 'oa-card-head');
  head.appendChild(el('h3', 'oa-drawer-subhead', t('secCards')));
  head.appendChild(el('span', 'oa-header-spacer'));

  const redeemRow = el('div', 'oa-redeem-row oa-input-row');
  redeemRow.hidden = true;
  const add = iconButton('oa-icon-btn', ICONS.plus, t('redeemAdd'), () => {
    redeemRow.hidden = !redeemRow.hidden;
    if (!redeemRow.hidden) input.focus();
  }, 15);
  head.appendChild(add);
  wrap.appendChild(head);

  const flash = el('p', 'oa-field-hint');
  const list = el('div', 'oa-card-list');

  const input = el('input');
  input.type = 'text';
  input.placeholder = t('redeemPlaceholder');
  input.spellcheck = false;
  const go = button('oa-btn', t('redeemAction'), () => void redeem());
  redeemRow.appendChild(input);
  redeemRow.appendChild(go);
  input.addEventListener('keydown', (event) => {
    if (event.key === 'Enter') {
      event.preventDefault();
      void redeem();
    }
  });

  wrap.appendChild(redeemRow);
  wrap.appendChild(flash);
  wrap.appendChild(list);

  async function redeem(): Promise<void> {
    const code = input.value.trim();
    if (!code) return;
    go.disabled = true;
    try {
      await api.post<{ card: Card }>('/api/usage/redeem', { code });
      input.value = '';
      redeemRow.hidden = true;
      flash.textContent = t('redeemed');
      await refresh();
    } catch (error) {
      flash.textContent = error instanceof ApiError ? error.message : String(error);
    } finally {
      go.disabled = false;
    }
  }

  async function use(card: Card, trigger: HTMLButtonElement): Promise<void> {
    trigger.disabled = true;
    try {
      await api.post<void>(`/api/usage/cards/${encodeURIComponent(card.id)}/use`, {});
      flash.textContent = t('cardUsed');
      celebrate();
      // The whole screen, not just this list: the bars above are the reason
      // somebody spent it, and leaving them at yesterday's figure would make
      // the card look like it did nothing.
      navigate('/usage');
    } catch (error) {
      trigger.disabled = false;
      flash.textContent = error instanceof ApiError ? error.message : String(error);
    }
  }

  async function refresh(): Promise<void> {
    clear(list);
    try {
      const { cards } = await api.get<{ cards: Card[] }>('/api/usage/cards');
      if (!cards.length) {
        list.appendChild(el('p', 'oa-field-hint', t('cardNone')));
        return;
      }
      for (const card of cards) {
        const row = el('div', 'oa-card-row');
        const text = el('div');
        text.appendChild(el('span', 'oa-card-title', t('cardFullReset')));
        text.appendChild(el('span', 'oa-card-sub', t('cardExpires', { when: expiry(card.expires_at) })));
        row.appendChild(text);
        row.appendChild(el('span', 'oa-header-spacer'));
        const spend = button('oa-btn', t('cardUse'), () => void use(card, spend));
        row.appendChild(spend);
        list.appendChild(row);
      }
    } catch {
      list.appendChild(el('p', 'oa-field-hint', t('usageUnavailable')));
    }
  }

  void refresh();
  return wrap;
}

function expiry(at: number): string {
  return new Date(at).toLocaleString(undefined, {
    month: 'numeric', day: 'numeric', hour: '2-digit', minute: '2-digit',
  });
}

function paintAllowance(host: HTMLElement, summary: UsageSummary | null): void {
  if (!summary) {
    host.appendChild(el('p', 'oa-field-hint', t('usageUnavailable')));
    return;
  }
  const enforced = summary.windows.filter((window) => window.enforced);
  if (summary.unlimited || !enforced.length) {
    host.appendChild(el('span', 'oa-usage-reset', t('quotaUnlimited')));
    return;
  }
  for (const window of enforced) {
    // The large form here, the compact one in the composer: this screen is
    // where somebody has come to look at exactly this, and a 4px hairline is
    // what you draw when the reader is halfway through a sentence.
    host.appendChild(usageWindow(window, summary.display ?? 'absolute', 'large'));
  }
}

function paintTotals(host: HTMLElement, totals: Totals): void {
  const stat = (label: string, value: string, note?: string) => {
    const card = el('div', 'oa-stat');
    card.appendChild(el('span', 'oa-stat-label', label));
    card.appendChild(el('span', 'oa-stat-value', value));
    if (note) card.appendChild(el('span', 'oa-stat-note', note));
    host.appendChild(card);
  };
  stat(t('statRequests'), compactNumber(totals.requests),
    totals.errors ? t('nFailed', { count: totals.errors }) : t('allFine'));
  stat(t('statTokens'), compactNumber(totals.total_tokens),
    t('tokensInOut', { input: compactNumber(totals.input_tokens), output: compactNumber(totals.output_tokens) }));
  stat(t('statCredits'), compactNumber(totals.credits));
}

function turnTable(turns: Turn[]): HTMLElement {
  return renderTable({
    columns: [
      { header: t('colModel'), cell: (row) => row.model_name },
      {
        header: t('colWhen'),
        cell: (row) => relativeTime(row.started_at),
        secondary: true,
        width: '110px',
      },
      {
        header: t('statTokens'),
        cell: (row) => compactNumber(row.total_tokens),
        numeric: true,
        width: '80px',
      },
      {
        header: t('statCredits'),
        // Two decimals, not the compact form: these are small numbers and
        // "0.1k" for a tenth of a credit would be a worse answer than none.
        cell: (row) => (Math.round(row.credits * 100) / 100).toFixed(2),
        numeric: true,
        width: '80px',
      },
      {
        header: t('colState'),
        cell: (row) => (row.status === 'ok' ? '' : t(`turn_${row.status}` as 'turn_error')),
        width: '80px',
      },
    ],
    rows: turns,
    empty: t('noTurnsYet'),
    muted: (row) => row.status !== 'ok',
  });
}
