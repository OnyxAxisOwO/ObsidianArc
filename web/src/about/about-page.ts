// The About screen.
//
// Rendered as a slide-in right panel over the chat rather than a full page,
// matching the settings panel. Closing it returns to the conversation still
// sitting behind it.

import { formatUptime } from '../admin/admin-page';
import { health } from '../api/client';
import { renderChatPage } from '../chat/chat-page';
import { t } from '../i18n';
import { navigate } from '../router';
import { siteInfo } from '../session';
import { el } from '../ui/dom';
import { section } from '../ui/form';
import { openPanel } from '../ui/panel';

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
