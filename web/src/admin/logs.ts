// The request log: everything the server answered, filterable.
//
// Separate from the usage screen, which is about spend. This one answers
// "what happened" — and the requests worth looking for are usually the ones
// that cost nothing because they were refused, which never reach the ledger
// at all.
//
// The filter controls are built from what is actually in the log rather than
// from every value that could theoretically appear, so an operator picks from
// a list of things that happened. That also means the controls are rebuilt
// when the window changes: last hour and last month have different casts.

import { ApiError } from '../api/client';
import { t, type StringKey } from '../i18n';
import { ICONS, button, clear, el, iconButton } from '../ui/dom';
import { selectField, textField } from '../ui/form';
import { openPanel } from '../ui/panel';
import { absoluteTime, badge, relativeTime } from '../ui/table';
import { adminApi, type LogEntry, type LogFacets, type LogOption } from './api';
import { failure, type AdminView } from './admin-page';

const PAGE_SIZE = 50;

/** Windows offered for "since". Empty is everything, which is the point of a
 *  log that is never pruned. */
const WINDOWS: Array<{ value: string; label: StringKey; hours: number }> = [
  { value: '1', label: 'lastHour', hours: 1 },
  { value: '24', label: 'last24h', hours: 24 },
  { value: '168', label: 'last7d', hours: 168 },
  { value: '720', label: 'last30d', hours: 720 },
  { value: '', label: 'allTime', hours: 0 },
];

interface Query {
  window: string;
  userID: string;
  modelID: string;
  outcome: string;
  status: string;
  channel: string;
  method: string;
  errorCode: string;
  path: string;
  offset: number;
}

export async function renderLogs(view: AdminView): Promise<void> {
  view.setTitle(t('navLogs'));

  const query: Query = {
    window: '24', userID: '', modelID: '', outcome: '', status: '',
    channel: '', method: '', errorCode: '', path: '', offset: 0,
  };

  let facets: LogFacets | null = null;

  const controls = el('div', 'oa-log-filters');
  const results = el('div', 'oa-log-results');

  clear(view.body);
  const page = el('div', 'oa-log-page');
  page.appendChild(controls);
  page.appendChild(results);
  view.body.appendChild(page);

  clear(view.actions);
  view.actions.appendChild(iconButton('oa-icon-btn', ICONS.chevron, t('refresh'), () => {
    void reload();
  }, 16));

  await reload();

  async function reload(): Promise<void> {
    try {
      // The facets follow the window: which accounts and models appear in
      // the last hour is a different list from the last month's.
      facets = await adminApi.logFacets(sinceQuery());
    } catch (error) {
      failure(view, error);
      return;
    }
    paintControls();
    await paintResults();
  }

  function since(): number {
    const entry = WINDOWS.find((w) => w.value === query.window);
    if (!entry || entry.hours === 0) return 0;
    return Date.now() - entry.hours * 60 * 60 * 1000;
  }

  function sinceQuery(): string {
    const at = since();
    return at > 0 ? `?since=${at}` : '';
  }

  function listQuery(): string {
    const params = new URLSearchParams();
    const at = since();
    if (at > 0) params.set('since', String(at));
    if (query.userID) params.set('user_id', query.userID);
    if (query.modelID) params.set('model_id', query.modelID);
    if (query.outcome) params.set('outcome', query.outcome);
    if (query.status) params.set('status', query.status);
    if (query.channel) params.set('channel', query.channel);
    if (query.method) params.set('method', query.method);
    if (query.errorCode) params.set('error_code', query.errorCode);
    if (query.path) params.set('path', query.path);
    params.set('limit', String(PAGE_SIZE));
    params.set('offset', String(query.offset));
    return `?${params.toString()}`;
  }

  // --- the filter bar ---------------------------------------------------------

  function paintControls(): void {
    clear(controls);
    if (!facets) return;

    const change = (apply: (value: string) => void) => (value: string) => {
      apply(value);
      // Any change to what is being asked invalidates which page we are on.
      query.offset = 0;
      void paintResults();
    };

    const window = selectField({
      label: t('logWindow'),
      value: query.window,
      options: WINDOWS.map((entry) => ({ value: entry.value, label: t(entry.label) })),
      onChange: (value) => {
        query.window = value;
        query.offset = 0;
        // The facets are window-scoped, so this is a full reload rather than
        // a re-query.
        void reload();
      },
    });

    const outcome = selectField({
      label: t('logOutcome'),
      value: query.outcome,
      options: [
        { value: '', label: t('logAnyOutcome') },
        { value: 'ok', label: t('logSucceeded') },
        { value: 'failed', label: t('logFailed') },
      ],
      onChange: change((value) => { query.outcome = value; }),
    });

    const user = selectField({
      label: t('logUser'),
      value: query.userID,
      options: withAny(facets.users, t('logAnyUser')),
      onChange: change((value) => { query.userID = value; }),
    });

    const model = selectField({
      label: t('logModel'),
      value: query.modelID,
      options: withAny(facets.models, t('logAnyModel')),
      onChange: change((value) => { query.modelID = value; }),
    });

    const status = selectField({
      label: t('logStatus'),
      value: query.status,
      options: withAny(facets.statuses, t('logAnyStatus')),
      onChange: change((value) => { query.status = value; }),
    });

    const errorCode = selectField({
      label: t('logErrorCode'),
      value: query.errorCode,
      options: withAny(facets.error_codes, t('logAnyErrorCode')),
      onChange: change((value) => { query.errorCode = value; }),
    });

    const channel = selectField({
      label: t('logChannel'),
      value: query.channel,
      options: [
        { value: '', label: t('logAnyChannel') },
        { value: 'web', label: t('logChannelWeb') },
        { value: 'api', label: t('logChannelAPI') },
      ],
      onChange: change((value) => { query.channel = value; }),
    });

    const path = textField({ label: t('logPath'), value: query.path, placeholder: '/api/chat' });
    const search = button('oa-btn', t('logApply'), () => {
      query.path = path.value();
      query.offset = 0;
      void paintResults();
    });

    const grid = el('div', 'oa-log-filter-grid');
    for (const control of [window, outcome, user, model, status, errorCode, channel, path]) {
      grid.appendChild(control.element);
    }
    controls.appendChild(grid);

    const row = el('div', 'oa-log-filter-actions');
    row.appendChild(search);
    row.appendChild(button('oa-btn', t('logClear'), () => {
      Object.assign(query, {
        userID: '', modelID: '', outcome: '', status: '',
        channel: '', method: '', errorCode: '', path: '', offset: 0,
      });
      paintControls();
      void paintResults();
    }));

    const summary = el('span', 'oa-field-hint');
    summary.textContent = t('logHolding', { count: facets.total });
    row.appendChild(summary);

    // A gap in an audit trail has to be visible, not inferred.
    if (facets.dropped > 0) {
      row.appendChild(badge(t('logDropped', { count: facets.dropped }), 'danger'));
    }
    controls.appendChild(row);
  }

  function withAny(options: LogOption[], anyLabel: string): Array<{ value: string; label: string }> {
    return [
      { value: '', label: anyLabel },
      ...options.map((option) => ({
        value: option.value,
        label: `${option.label} (${option.count})`,
      })),
    ];
  }

  // --- the listing ------------------------------------------------------------

  async function paintResults(): Promise<void> {
    clear(results);
    results.appendChild(el('p', 'oa-menu-empty', t('loading')));

    let data;
    try {
      data = await adminApi.logs(listQuery());
    } catch (error) {
      clear(results);
      results.appendChild(el('p', 'oa-menu-empty',
        error instanceof ApiError ? error.message : t('failed')));
      return;
    }

    clear(results);
    if (!data.entries.length) {
      results.appendChild(el('p', 'oa-menu-empty', t('logEmpty')));
      return;
    }

    const list = el('div', 'oa-log-list');
    for (const entry of data.entries) list.appendChild(row(entry));
    results.appendChild(list);

    const first = data.offset + 1;
    const last = data.offset + data.entries.length;
    const pager = el('div', 'oa-log-pager');
    pager.appendChild(el('span', 'oa-field-hint',
      t('logRange', { first, last, total: data.total })));

    const back = button('oa-btn', t('previous'), () => {
      query.offset = Math.max(0, query.offset - PAGE_SIZE);
      void paintResults();
    });
    back.disabled = data.offset === 0;
    const forward = button('oa-btn', t('next'), () => {
      query.offset += PAGE_SIZE;
      void paintResults();
    });
    forward.disabled = last >= data.total;

    pager.appendChild(back);
    pager.appendChild(forward);
    results.appendChild(pager);
  }

  function row(entry: LogEntry): HTMLElement {
    const item = el('button', 'oa-log-row');
    item.type = 'button';

    const tone = entry.status >= 500 ? 'danger' : entry.status >= 400 ? 'muted' : 'default';
    item.appendChild(badge(String(entry.status), tone));

    const main = el('div', 'oa-log-row-main');
    const head = el('div', 'oa-log-row-head');
    head.appendChild(el('span', 'oa-log-method', entry.method));
    head.appendChild(el('span', 'oa-log-path', entry.path));
    main.appendChild(head);

    const meta = el('div', 'oa-log-row-meta');
    meta.appendChild(el('span', null, entry.username || t('logAnonymous')));
    if (entry.model_name) meta.appendChild(el('span', null, entry.model_name));
    if (entry.error_code) meta.appendChild(el('span', 'oa-log-error', entry.error_code));
    meta.appendChild(el('span', null, `${entry.duration_ms} ms`));
    meta.appendChild(el('span', null, relativeTime(entry.at)));
    main.appendChild(meta);

    item.appendChild(main);
    item.addEventListener('click', () => openEntry(view, entry));
    return item;
  }
}

/** The whole record, for the one request somebody is actually asking about. */
function openEntry(view: AdminView, entry: LogEntry): void {
  openPanel({
    host: view.host,
    title: `${entry.method} ${entry.path}`,
    footer: false,
    width: 460,
    build: (body) => {
      const facts: Array<[StringKey, string]> = [
        ['logStatus', String(entry.status)],
        ['logWhen', absoluteTime(entry.at)],
        ['logDuration', `${entry.duration_ms} ms`],
        ['logBytes', String(entry.bytes)],
        ['logUser', entry.username || t('logAnonymous')],
        ['logChannel', entry.channel || '—'],
        ['logModel', entry.model_name || '—'],
        ['logErrorCode', entry.error_code || '—'],
        ['logIP', entry.ip || '—'],
        ['logRequestID', entry.request_id || '—'],
        ['logUserAgent', entry.user_agent || '—'],
      ];

      const list = el('dl', 'oa-log-facts');
      for (const [label, value] of facts) {
        list.appendChild(el('dt', null, t(label)));
        list.appendChild(el('dd', null, value));
      }
      body.appendChild(list);
    },
  });
}
