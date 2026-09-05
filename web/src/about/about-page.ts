// The About screen.
//
// A normal signed-in page rather than a panel: there is nothing here to edit
// and nothing to close back to, so it draws straight into the shell body the
// way the admin pages do, minus the rail — one reading column is the whole
// layout this content needs.

import { formatUptime } from '../admin/admin-page';
import { health } from '../api/client';
import { renderShell } from '../app/shell';
import { t } from '../i18n';
import { siteInfo } from '../session';
import { el } from '../ui/dom';
import { section } from '../ui/form';

const SOURCE_URL = 'https://github.com/OnyxAxisOwO/ObsidianArc';

export function renderAboutPage(root: HTMLElement): void {
  const shell = renderShell(root);
  shell.body.classList.add('oa-admin');

  const main = el('div', 'oa-admin-main');
  const head = el('div', 'oa-admin-head');
  head.appendChild(el('h1', 'oa-admin-title', t('about')));
  head.appendChild(el('span', 'oa-admin-head-spacer'));

  const body = el('div', 'oa-admin-body');
  main.appendChild(head);
  main.appendChild(body);
  shell.body.appendChild(main);

  const wrap = el('div', 'oa-about');
  body.appendChild(wrap);

  const intro = el('div', 'oa-about-head');
  intro.appendChild(el('h2', 'oa-about-name', siteInfo().name));
  intro.appendChild(el('p', 'oa-about-lede', t('aboutBody')));
  wrap.appendChild(intro);

  const facts = el('div', 'oa-about-facts');
  const version = aboutFact(t('aboutVersion'), '—');
  const uptime = aboutFact(t('aboutRuntime'), '—');
  facts.appendChild(version.row);
  facts.appendChild(uptime.row);
  facts.appendChild(aboutFact(t('aboutLicense'), 'MIT').row);

  // In the facts box rather than above it, and showing the address rather
  // than the word "Source": on its own line in the body copy's weight it read
  // as another heading, not as somewhere to go.
  const sourceRow = el('div', 'oa-about-fact');
  sourceRow.appendChild(el('span', 'oa-about-fact-label', t('aboutSource')));
  const source = el('a', 'oa-about-fact-value oa-about-link', SOURCE_URL.replace('https://', ''));
  source.href = SOURCE_URL;
  source.target = '_blank';
  source.rel = 'noopener noreferrer';
  sourceRow.appendChild(source);
  facts.appendChild(sourceRow);

  wrap.appendChild(facts);

  wrap.appendChild(section(t('aboutBuiltWith'), t('aboutBuiltWithBody')));

  void health()
    .then((status) => {
      version.value.textContent = status.version;
      uptime.value.textContent = formatUptime(status.uptime_sec);
    })
    .catch(() => {
      // The facts box just keeps its placeholders; nothing else on the page
      // depends on the server being reachable.
    });
}

function aboutFact(label: string, value: string): { row: HTMLElement; value: HTMLElement } {
  const row = el('div', 'oa-about-fact');
  row.appendChild(el('span', 'oa-about-fact-label', label));
  const valueNode = el('span', 'oa-about-fact-value', value);
  row.appendChild(valueNode);
  return { row, value: valueNode };
}
