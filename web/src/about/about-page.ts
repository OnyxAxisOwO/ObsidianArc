// The About screen.
//
// Rendered as a slide-in right panel over the chat rather than a full page,
// matching the settings panel. Closing it returns to the conversation still
// sitting behind it.

import { health } from '../api/client';
import { renderChatPage } from '../chat/chat-page';
import { t } from '../i18n';
import { navigate } from '../router';
import { siteInfo } from '../session';
import { el } from '../ui/dom';
import { section } from '../ui/form';
import { openPanel } from '../ui/panel';
import { formatUptime } from '../ui/table';

// An instance can be renamed and can describe itself however its operator
// wants, and the heading and body above honour that. These two do not: they
// name the software rather than the deployment, so a rebranded server still
// answers "what am I actually running, and where did it come from" — which is
// the question the About panel exists for.
const PRODUCT = 'Obsidian Arc';
const SOURCE_URL = 'https://github.com/OnyxAxisOwO/ObsidianArc';

export function renderAboutPage(root: HTMLElement): void {
  const host = renderChatPage(root);

  openPanel({
    host,
    title: t('about'),
    footer: false,
    width: 460,
    build: (body) => {
      const wrap = el('div', 'oa-about');

      // Both fall back rather than render empty: an operator who has never
      // opened the settings screen still gets a finished page.
      const about = siteInfo().about;
      const intro = el('div', 'oa-about-head');
      intro.appendChild(el('h2', 'oa-about-name', about?.title?.trim() || siteInfo().name));
      intro.appendChild(el('p', 'oa-about-lede', about?.body?.trim() || t('aboutBody')));
      wrap.appendChild(intro);

      const facts = el('div', 'oa-about-facts');
      const version = aboutFact(t('aboutVersionOf', { product: PRODUCT }), '—');
      const uptime = aboutFact(t('aboutRuntime'), '—');
      facts.appendChild(version.row);

      // Directly under the version, and showing the address rather than the
      // word "Source": the two together are what identifies the software when
      // everything above them has been rewritten. On its own line in the body
      // copy's weight it read as another heading, not as somewhere to go.
      const sourceRow = el('div', 'oa-about-fact');
      sourceRow.appendChild(el('span', 'oa-about-fact-label', t('aboutSource')));
      const source = el('a', 'oa-about-fact-value oa-about-link', SOURCE_URL.replace('https://', ''));
      source.href = SOURCE_URL;
      source.target = '_blank';
      source.rel = 'noopener noreferrer';
      sourceRow.appendChild(source);
      facts.appendChild(sourceRow);

      facts.appendChild(uptime.row);
      facts.appendChild(aboutFact(t('aboutLicense'), 'MIT').row);

      wrap.appendChild(facts);
      wrap.appendChild(section(t('aboutBuiltWith'), t('aboutBuiltWithBody')));
      body.appendChild(wrap);

      void health()
        .then((status) => {
          version.value.textContent = status.version;
          uptime.value.textContent = formatUptime(status.uptime_sec);
        })
        .catch(() => {
          // The facts box just keeps its placeholders; nothing else on the page
          // depends on the server being reachable.
        });
    },
    onClose: () => {
      if (window.location.pathname === '/about') navigate('/', { replace: true });
    },
  });
}

function aboutFact(label: string, value: string): { row: HTMLElement; value: HTMLElement } {
  const row = el('div', 'oa-about-fact');
  row.appendChild(el('span', 'oa-about-fact-label', label));
  const valueNode = el('span', 'oa-about-fact-value', value);
  row.appendChild(valueNode);
  return { row, value: valueNode };
}
