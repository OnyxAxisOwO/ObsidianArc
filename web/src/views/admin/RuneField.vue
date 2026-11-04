<script setup lang="ts">
import { onBeforeUnmount, onMounted, ref } from 'vue';
import { runeIntensity } from '@/admin/rune-field';

const canvas = ref<HTMLCanvasElement | null>(null);
const RAMP = ' .:+*#%@';
const ripples: Array<{ x: number; y: number; born: number }> = [];
let ctx: CanvasRenderingContext2D | null = null;
let width = 0;
let height = 0;
let frame = 0;
let last = 0;
let still = false;
let observer: ResizeObserver | undefined;
let host: HTMLElement | null = null;

function draw(now: number): void {
  if (!ctx || !canvas.value) return;
  const cols = Math.ceil(width / 9);
  const rows = Math.ceil(height / 14);
  ctx.clearRect(0, 0, width, height);
  ctx.fillStyle = getComputedStyle(canvas.value).color;
  ctx.font = '11px ui-monospace, SFMono-Regular, Menlo, monospace';
  ctx.textBaseline = 'top';
  while (ripples.length && now - ripples[0]!.born > 1800) ripples.shift();
  for (let y = 0; y < rows; y += 1) {
    for (let x = 0; x < cols; x += 1) {
      let value = runeIntensity(x, y, cols, rows, now / 1000);
      for (const ripple of ripples) {
        const age = (now - ripple.born) / 1000;
        // Squared rings echo the field's geometry and keep its clicks
        // distinct from the circular ripples of an ordinary conversation.
        const distance = Math.max(Math.abs(x * 9 - ripple.x), Math.abs(y * 14 - ripple.y));
        value += Math.max(0, 1 - Math.abs(distance - age * 180) / 16) * (1 - age / 1.8) * 0.8;
      }
      if (value < 0.1) continue;
      const level = Math.min(RAMP.length - 1, Math.max(1, Math.round(value * (RAMP.length - 1))));
      ctx.globalAlpha = 0.12 + Math.min(0.42, value * 0.42);
      ctx.fillText(RAMP[level]!, x * 9, y * 14);
    }
  }
  ctx.globalAlpha = 1;
}

function resize(): void {
  if (!canvas.value || !ctx) return;
  const box = canvas.value.getBoundingClientRect();
  width = box.width;
  height = box.height;
  const ratio = window.devicePixelRatio || 1;
  canvas.value.width = Math.round(width * ratio);
  canvas.value.height = Math.round(height * ratio);
  ctx.setTransform(ratio, 0, 0, ratio, 0, 0);
  draw(performance.now());
}

function loop(now: number): void {
  frame = requestAnimationFrame(loop);
  if (now - last < 1000 / 30 || document.hidden) return;
  last = now;
  draw(now);
}

function pointer(event: PointerEvent): void {
  if (still || !canvas.value) return;
  const box = canvas.value.getBoundingClientRect();
  ripples.push({ x: event.clientX - box.left, y: event.clientY - box.top, born: performance.now() });
  if (ripples.length > 6) ripples.shift();
}

onMounted(() => {
  ctx = canvas.value?.getContext('2d') ?? null;
  if (!ctx || !canvas.value) return;
  still = !!window.matchMedia?.('(prefers-reduced-motion: reduce)').matches;
  observer = typeof ResizeObserver === 'function' ? new ResizeObserver(resize) : undefined;
  observer?.observe(canvas.value);
  host = canvas.value.parentElement;
  host?.addEventListener('pointerdown', pointer);
  resize();
  if (!still) frame = requestAnimationFrame(loop);
});

onBeforeUnmount(() => {
  cancelAnimationFrame(frame);
  observer?.disconnect();
  host?.removeEventListener('pointerdown', pointer);
});
</script>

<template><canvas ref="canvas" class="ai-ascii-field oa-rune-field" aria-hidden="true" /></template>
