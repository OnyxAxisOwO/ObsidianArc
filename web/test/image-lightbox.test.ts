// The lightbox writes its source into both the picture and the download link.
// A javascript: link runs in the app's own origin when it is clicked, so only
// the forms an image is served under may reach the page.

import { afterEach, describe, expect, it } from 'vitest';
import { createApp, h, nextTick, type App } from 'vue';
import OaImageLightbox from '../src/components/OaImageLightbox.vue';
import { t } from '../src/composables/useI18n';

let app: App | undefined;

afterEach(() => {
  app?.unmount();
  app = undefined;
});

// Not mountComponent: the tests use the screen the moment it is mounted, without awaiting, and read its first render.
function mount(src: string): void {
  const host = document.createElement('div');
  document.body.append(host);
  app = createApp({ render: () => h(OaImageLightbox, { src, alt: 'A picture', downloadName: 'a.png' }) });
  app.mount(host);
}

describe('OaImageLightbox source', () => {
  it.each([
    'javascript:alert(1)',
    'JavaScript:alert(1)',
    'java\tscript:alert(1)',
    ' java\nscript:alert(1)',
    'data:text/html;base64,PHNjcmlwdD4=',
    'vbscript:msgbox(1)',
    '\u00a0javascript:alert(1)',
    '\ufeffjavascript:alert(1)',
    '\u3000data:text/html;base64,PHNjcmlwdD4=',
  ])('draws no picture and no download link for %j', async (src) => {
    mount(src);
    await nextTick();
    expect(document.querySelector('.oa-lightbox-img')).toBeNull();
    expect(document.querySelector('.oa-lightbox-actions')).toBeNull();
    expect(document.querySelector('a[href]')).toBeNull();
  });

  it.each([
    '/api/attachments/one.png',
    'https://images.example.test/one.png',
    'blob:https://example.test/0f1e2d',
    'data:image/png;base64,iVBORw0KGgo=',
  ])('uses %j for the picture and the download', async (src) => {
    mount(src);
    await nextTick();
    expect(document.querySelector<HTMLImageElement>('.oa-lightbox-img')?.getAttribute('src')).toBe(src);
    expect(document.querySelector<HTMLAnchorElement>('.oa-lightbox-actions a')?.getAttribute('href')).toBe(src);
  });
});

// A screen reader arriving on the sheet hears its name, and a picture with no
// description still needs one to say what the sheet is.
describe('OaImageLightbox names its dialog', () => {
  it('after the picture it shows', () => {
    mount('/api/attachments/one.png');
    expect(document.querySelector('[role="dialog"]')?.getAttribute('aria-label')).toBe('A picture');
  });

  it('after the enlarge line when the picture has no description', () => {
    const host = document.createElement('div');
    document.body.append(host);
    app = createApp({ render: () => h(OaImageLightbox, { src: '/api/attachments/one.png' }) });
    app.mount(host);
    expect(document.querySelector('[role="dialog"]')?.getAttribute('aria-label')).toBe(t('zoomImage'));
  });
});
