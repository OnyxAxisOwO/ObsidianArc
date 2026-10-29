<script setup lang="ts">
// Characters drifting behind an empty conversation, and rings where it is
// clicked.
//
// A canvas rather than a grid of text nodes: ten thousand cells redrawn thirty
// times a second as DOM is a layout per frame, and this is decoration. It
// exists only while the conversation is empty — the first message unmounts it,
// so a chat never pays for it — and it draws in the text colour at low alpha,
// so it follows the theme and the accent without a colour of its own.
//
// The shapes are a few wanderers trailing tails. Each is a point on a slow
// sum of sines, and its recent positions are splatted into an intensity grid
// that the character ramp then reads; a click pushes them away and sends a
// ring outwards from where it landed.

import { onBeforeUnmount, onMounted, ref } from 'vue';

const canvas = ref<HTMLCanvasElement | null>(null);

const RAMP = ' .·:-=+*#%@';
const CELL_W = 9;
const CELL_H = 14;
const TRAIL = 34;
const WANDERERS = 8;
// Thirty frames a second is smooth for something this slow, and half what a
// display asks for: the composer under it is what has to feel instant.
const FRAME_MS = 1000 / 30;

interface Wanderer {
  x: number;
  y: number;
  vx: number;
  vy: number;
  phase: number;
  // How big and how quick this one is, so a shoal is not eight copies.
  size: number;
  pace: number;
  trail: { x: number; y: number }[];
}

interface Ripple {
  x: number;
  y: number;
  born: number;
}

let ctx: CanvasRenderingContext2D | null = null;
let cols = 0;
let rows = 0;
let field = new Float32Array(0);
let dust = new Float32Array(0);
let wanderers: Wanderer[] = [];
const ripples: Ripple[] = [];
let frame = 0;
let last = 0;
let colour = '';
let colourReadAt = 0;
let still = false;
let observer: ResizeObserver | null = null;
let host: HTMLElement | null = null;

function seed(): void {
  // Scattered over the field rather than started in a row, so the first
  // frame already looks like it has been going a while.
  wanderers = Array.from({ length: WANDERERS }, (_, i) => ({
    x: cols * ((i * 0.618 + 0.1) % 1),
    y: rows * ((i * 0.382 + 0.15) % 1),
    vx: 0,
    vy: 0,
    phase: i * 2.1,
    size: 0.55 + ((i * 0.37) % 1) * 0.75,
    pace: 0.7 + ((i * 0.53) % 1) * 0.6,
    trail: [],
  }));
}

function resize(): void {
  const node = canvas.value;
  if (!node || !ctx) return;
  const ratio = window.devicePixelRatio || 1;
  const { width, height } = node.getBoundingClientRect();
  node.width = Math.round(width * ratio);
  node.height = Math.round(height * ratio);
  ctx.setTransform(ratio, 0, 0, ratio, 0, 0);
  const nextCols = Math.ceil(width / CELL_W);
  const nextRows = Math.ceil(height / CELL_H);
  if (nextCols === cols && nextRows === rows) return;
  cols = nextCols;
  rows = nextRows;
  field = new Float32Array(cols * rows);
  // A sparse, fixed scatter of faint dots: the field is never quite empty, so
  // the wanderers read as moving through something rather than on nothing.
  dust = new Float32Array(cols * rows);
  for (let i = 0; i < dust.length; i += 1) dust[i] = Math.random() < 0.035 ? 0.12 + Math.random() * 0.1 : 0;
  seed();
  if (still) draw(performance.now());
}

function step(now: number): void {
  const t = now / 1000;
  for (const w of wanderers) {
    // Steering by a sum of incommensurate sines: smooth, never repeating
    // within a sitting, and no random walk's jitter.
    const ax = Math.sin(t * 0.31 + w.phase) + 0.6 * Math.sin(t * 0.17 + w.phase * 1.7);
    const ay = Math.cos(t * 0.23 + w.phase * 0.8) + 0.6 * Math.sin(t * 0.41 + w.phase);
    w.vx = w.vx * 0.94 + ax * 0.035 * w.pace;
    w.vy = w.vy * 0.94 + ay * 0.022 * w.pace;
    w.x += w.vx;
    w.y += w.vy;
    // Wrap rather than bounce, past the edge by a tail's length, so nothing
    // visibly turns around at a wall.
    const pad = 12;
    if (w.x < -pad) w.x = cols + pad;
    if (w.x > cols + pad) w.x = -pad;
    if (w.y < -pad) w.y = rows + pad;
    if (w.y > rows + pad) w.y = -pad;
    w.trail.unshift({ x: w.x, y: w.y });
    if (w.trail.length > TRAIL) w.trail.pop();
  }
}

function splat(cx: number, cy: number, radius: number, strength: number): void {
  const x0 = Math.max(0, Math.floor(cx - radius));
  const x1 = Math.min(cols - 1, Math.ceil(cx + radius));
  const y0 = Math.max(0, Math.floor(cy - radius / 1.6));
  const y1 = Math.min(rows - 1, Math.ceil(cy + radius / 1.6));
  const r2 = radius * radius;
  for (let y = y0; y <= y1; y += 1) {
    // Cells are taller than wide, so a round blob in cells is an oval on
    // screen; stretching y by the cell's aspect makes it round again.
    const dy = (y - cy) * 1.6;
    for (let x = x0; x <= x1; x += 1) {
      const dx = x - cx;
      const d2 = dx * dx + dy * dy;
      if (d2 < r2) field[y * cols + x]! += strength * (1 - d2 / r2);
    }
  }
}

function draw(now: number): void {
  const node = canvas.value;
  if (!node || !ctx) return;
  if (!colour || now - colourReadAt > 1000) {
    // Read again now and then rather than watched: the theme and the accent
    // change rarely, and a second's lag on a decoration is invisible.
    colour = getComputedStyle(node).color;
    colourReadAt = now;
  }
  field.set(dust);
  for (const w of wanderers) {
    w.trail.forEach((p, i) => {
      const k = 1 - i / TRAIL;
      splat(p.x, p.y, (1.6 + 3.4 * k) * w.size, 0.26 * k);
    });
  }
  for (let r = ripples.length - 1; r >= 0; r -= 1) {
    const ripple = ripples[r]!;
    const age = (now - ripple.born) / 1000;
    if (age > 1.8) {
      ripples.splice(r, 1);
      continue;
    }
    const radius = age * 26;
    const fade = 1 - age / 1.8;
    const reach = radius + 2;
    const y0 = Math.max(0, Math.floor(ripple.y - reach / 1.6));
    const y1 = Math.min(rows - 1, Math.ceil(ripple.y + reach / 1.6));
    const x0 = Math.max(0, Math.floor(ripple.x - reach));
    const x1 = Math.min(cols - 1, Math.ceil(ripple.x + reach));
    for (let y = y0; y <= y1; y += 1) {
      const dy = (y - ripple.y) * 1.6;
      for (let x = x0; x <= x1; x += 1) {
        const dx = x - ripple.x;
        const off = Math.abs(Math.sqrt(dx * dx + dy * dy) - radius);
        if (off < 1.6) field[y * cols + x]! += 0.9 * fade * (1 - off / 1.6);
      }
    }
  }

  const { width, height } = node.getBoundingClientRect();
  ctx.clearRect(0, 0, width, height);
  ctx.fillStyle = colour;
  ctx.font = `11px ui-monospace, SFMono-Regular, Menlo, monospace`;
  ctx.textBaseline = 'top';
  // Grouped by level so the alpha is set eleven times a frame, not per cell.
  const levels = RAMP.length - 1;
  // A grain that moves across the field, so a wanderer's body is a texture of
  // several characters rather than one solid run of the densest.
  const t = now / 1000;
  for (let i = 0; i < field.length; i += 1) {
    const v = field[i]!;
    if (v <= 0.13) continue;
    const x = i % cols;
    const y = Math.floor(i / cols);
    field[i] = v * (0.62 + 0.38 * Math.sin(x * 0.9 + y * 1.7 + t * 1.6) * Math.cos(x * 0.37 - y * 0.8 - t));
  }
  for (let level = 1; level <= levels; level += 1) {
    ctx.globalAlpha = 0.12 + 0.4 * (level / levels);
    const char = RAMP[level]!;
    for (let i = 0; i < field.length; i += 1) {
      const v = Math.min(1, field[i]!);
      if (Math.round(v * levels) !== level) continue;
      ctx.fillText(char, (i % cols) * CELL_W, Math.floor(i / cols) * CELL_H);
    }
  }
  ctx.globalAlpha = 1;
}

function loop(now: number): void {
  frame = requestAnimationFrame(loop);
  if (now - last < FRAME_MS) return;
  last = now;
  step(now);
  draw(now);
}

function onPointer(event: PointerEvent): void {
  const node = canvas.value;
  if (!node || still) return;
  const box = node.getBoundingClientRect();
  const x = (event.clientX - box.left) / CELL_W;
  const y = (event.clientY - box.top) / CELL_H;
  ripples.push({ x, y, born: performance.now() });
  if (ripples.length > 6) ripples.shift();
  for (const w of wanderers) {
    const dx = w.x - x;
    const dy = (w.y - y) * 1.6;
    const d = Math.max(4, Math.hypot(dx, dy));
    if (d > 60) continue;
    w.vx += (dx / d) * (18 / d);
    w.vy += (dy / d) * (11 / d);
  }
}

onMounted(() => {
  const node = canvas.value;
  ctx = node?.getContext('2d') ?? null;
  if (!node || !ctx) return;
  still = !!window.matchMedia?.('(prefers-reduced-motion: reduce)').matches;
  observer = typeof ResizeObserver === 'function' ? new ResizeObserver(resize) : null;
  observer?.observe(node);
  resize();
  // The clicks land on the content above, never on the canvas, which takes no
  // pointer events so it cannot swallow one meant for a suggestion.
  host = node.parentElement;
  host?.addEventListener('pointerdown', onPointer);
  if (still) {
    for (let i = 0; i < TRAIL; i += 1) step(i * 120);
    draw(performance.now());
    return;
  }
  frame = requestAnimationFrame(loop);
});

onBeforeUnmount(() => {
  cancelAnimationFrame(frame);
  observer?.disconnect();
  host?.removeEventListener('pointerdown', onPointer);
});
</script>

<template>
  <canvas ref="canvas" class="ai-ascii-field" aria-hidden="true" />
</template>
