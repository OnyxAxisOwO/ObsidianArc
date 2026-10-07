import { describe, it, expect, vi, beforeEach } from 'vitest';
import { render, renderInto } from '../src/chat/markdown';
import {
  CANVAS_PATH, CANVAS_SANDBOX, canvasRun, canvasSource, closeCanvas, isRunnable, openCanvas, rerunCanvas,
} from '../src/chat/canvas';

function draw(markdown: string, run?: (source: string) => void): HTMLElement {
  const host = document.createElement('div');
  host.appendChild(render(markdown, { run }));
  return host;
}

const page = '<!doctype html><html><body><script>document.body.textContent = 1</script></body></html>';

describe('canvas', () => {
  beforeEach(() => closeCanvas());

  describe('which blocks are offered', () => {
    it('runs canvas and html fences, and nothing else', () => {
      expect(isRunnable('canvas')).toBe(true);
      expect(isRunnable('HTML')).toBe(true);
      expect(isRunnable('xhtml')).toBe(true);
      for (const tag of ['', 'js', 'javascript', 'css', 'svg', 'vue', 'python']) {
        expect(isRunnable(tag), tag).toBe(false);
      }
    });
  });

  describe('the run button', () => {
    it('is absent unless the caller opts in', () => {
      // Announcements, feedback and the About panel render through the same
      // function and must never invite a reader to run what they show.
      const host = draw('```canvas\n' + page + '\n```');
      expect(host.querySelector('.ai-code-run')).toBeNull();
    });

    it('is drawn on runnable blocks only, and hands over the block source', () => {
      const run = vi.fn();
      const host = draw(
        '```canvas\n' + page + '\n```\n\n```js\nconsole.log(1)\n```\n\n```html\n<p>hi</p>\n```',
        run,
      );
      const buttons = host.querySelectorAll<HTMLButtonElement>('.ai-code-run');
      expect(buttons).toHaveLength(2);
      buttons[0]!.click();
      expect(run).toHaveBeenCalledWith(page);
      buttons[1]!.click();
      expect(run).toHaveBeenLastCalledWith('<p>hi</p>');
    });

    it('keeps the source as text: nothing in the block becomes an element', () => {
      const host = draw('```canvas\n' + page + '\n```', () => {});
      expect(host.querySelectorAll('script, iframe, html, body').length).toBe(0);
      expect(host.querySelector('code')?.textContent).toBe(page);
    });

    it('survives a repaint that does not pass options, and goes when told to', () => {
      const run = vi.fn();
      const host = document.createElement('div');
      renderInto(host, '```html\n<p>x</p>\n```', { run });
      expect(host.querySelector('.ai-code-run')).not.toBeNull();
      // The math chunk repaints elements without knowing their options.
      renderInto(host, '```html\n<p>x</p>\n```');
      expect(host.querySelector('.ai-code-run')).not.toBeNull();
      // Switching Canvas off passes run: undefined explicitly.
      renderInto(host, '```html\n<p>x</p>\n```', { run: undefined });
      expect(host.querySelector('.ai-code-run')).toBeNull();
    });

    it('leaves no options behind for a later render', () => {
      draw('```html\n<p>x</p>\n```', () => {});
      expect(draw('```html\n<p>x</p>\n```').querySelector('.ai-code-run')).toBeNull();
    });
  });

  describe('state', () => {
    it('opens, reruns as a fresh document, and closes', () => {
      const before = canvasRun.value;
      openCanvas(page);
      expect(canvasSource.value).toBe(page);
      expect(canvasRun.value).toBe(before + 1);
      rerunCanvas();
      expect(canvasRun.value).toBe(before + 2);
      closeCanvas();
      expect(canvasSource.value).toBeNull();
      rerunCanvas();
      expect(canvasRun.value).toBe(before + 2);
    });
  });

  describe('the frame', () => {
    it('is sandboxed to scripts alone, never same-origin', () => {
      // With allow-same-origin a page served from this origin would run as
      // this origin: the reader's session and every endpoint they can call.
      expect(CANVAS_SANDBOX.split(/\s+/)).toEqual(['allow-scripts']);
      expect(CANVAS_PATH).toBe('/canvas/frame');
    });
  });
});
