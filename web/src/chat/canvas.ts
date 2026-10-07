// The Canvas: one page a model wrote, run in a sandboxed frame beside the
// conversation.
//
// State, not a route. The page is the text of a code block already on screen;
// putting it in the address would put a model's code in the history, in every
// shared link and in the server's request log, and the panel has nothing to
// fetch that a reload could restore anyway.
//
// The frame itself is served by the server at CANVAS_PATH with a policy of
// its own (internal/web/canvas.go). A srcdoc or blob: frame would inherit this
// page's policy, which allows no inline script, so a model's JavaScript would
// render and never run.

import { ref } from 'vue';

export const CANVAS_PATH = '/canvas/frame';

/**
 * What the frame's sandbox allows, and nothing more.
 *
 * allow-same-origin is the one that must never be here: with it, a document
 * served from this origin would run as this origin — the reader's session,
 * their storage, every endpoint they can call. Without it the page is an
 * opaque origin that can draw and compute and do nothing else.
 */
export const CANVAS_SANDBOX = 'allow-scripts';

/** The message the frame's shell waits for, and the one it sends when ready. */
export const RUN_MESSAGE = 'arc-canvas-run';
export const READY_MESSAGE = 'arc-canvas-ready';

/** The page currently in the Canvas, or null while it is closed. */
export const canvasSource = ref<string | null>(null);
/**
 * Bumped on every run, including a second run of the same page: the panel
 * keys its frame on it, so each run is a fresh document rather than a second
 * write into one the previous page has had its hands on.
 */
export const canvasRun = ref(0);

export function openCanvas(html: string): void {
  canvasSource.value = html;
  canvasRun.value += 1;
}

export function rerunCanvas(): void {
  if (canvasSource.value !== null) canvasRun.value += 1;
}

export function closeCanvas(): void {
  canvasSource.value = null;
}

/**
 * Whether a fenced block is something the Canvas can run.
 *
 * `canvas` is the tag the model is told to use for a page meant to be run;
 * plain `html` is offered too, because a reader looking at an HTML example
 * reasonably wants to see it — but only when they ask, which is the button.
 * CSS and JavaScript on their own are not pages, and wrapping them into one
 * would be guessing at what the author meant.
 */
export function isRunnable(tag: string): boolean {
  const lower = tag.trim().toLowerCase();
  return lower === 'canvas' || lower === 'html' || lower === 'xhtml';
}
