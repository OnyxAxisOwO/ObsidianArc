// Turns a picked/dropped/pasted image File into the {dataUrl, name, width,
// height} shape chat.js and store.js expect for an attachment.
//
// Downscaled through a canvas before it is ever stored: a phone photo can be
// 4000px and several megabytes, and every attachment is re-sent on every
// later turn of the conversation, so an oversized picture would multiply its
// cost on every message after the one that attached it.

(function (root, factory) {
  const api = factory();
  if (typeof module === 'object' && module.exports) module.exports = api;
  if (root) root.ObsidianImage = api;
})(typeof globalThis !== 'undefined' ? globalThis : this, function () {
  'use strict';

  const MAX_DIMENSION = 1568; // Anthropic's own guidance for the useful ceiling on an image's long edge.
  const MAX_OUTPUT_CHARS = 2 * 1024 * 1024;
  const JPEG_QUALITY_STEPS = [0.85, 0.7, 0.55, 0.4];

  function readAsDataUrl(file) {
    return new Promise((resolve, reject) => {
      const reader = new FileReader();
      reader.onload = () => resolve(String(reader.result || ''));
      reader.onerror = () => reject(new Error('That image could not be read.'));
      reader.readAsDataURL(file);
    });
  }

  function loadImage(dataUrl) {
    return new Promise((resolve, reject) => {
      const image = new Image();
      image.onload = () => resolve(image);
      image.onerror = () => reject(new Error('That image could not be decoded.'));
      image.src = dataUrl;
    });
  }

  function drawScaled(image, scale) {
    const canvas = document.createElement('canvas');
    canvas.width = Math.max(1, Math.round(image.naturalWidth * scale));
    canvas.height = Math.max(1, Math.round(image.naturalHeight * scale));
    const ctx = canvas.getContext('2d');
    ctx.drawImage(image, 0, 0, canvas.width, canvas.height);
    return canvas;
  }

  // GIF is kept as-is (a re-encode would lose animation); everything else is
  // normalized to JPEG, which compresses a photo far better than PNG does and
  // is accepted by both provider shapes.
  async function prepareChatImage(file) {
    if (!file || !file.type || !file.type.startsWith('image/')) {
      throw new Error('Not an image file.');
    }
    const original = await readAsDataUrl(file);
    if (file.type === 'image/gif') {
      if (original.length > MAX_OUTPUT_CHARS) throw new Error('That image is too large.');
      const probe = await loadImage(original);
      return { dataUrl: original, name: file.name || '', width: probe.naturalWidth, height: probe.naturalHeight };
    }

    const image = await loadImage(original);
    const longEdge = Math.max(image.naturalWidth, image.naturalHeight);
    const scale = longEdge > MAX_DIMENSION ? MAX_DIMENSION / longEdge : 1;
    const canvas = drawScaled(image, scale);

    for (const quality of JPEG_QUALITY_STEPS) {
      const dataUrl = canvas.toDataURL('image/jpeg', quality);
      if (dataUrl.length <= MAX_OUTPUT_CHARS) {
        return { dataUrl, name: file.name || '', width: canvas.width, height: canvas.height };
      }
    }
    throw new Error('That image is too large.');
  }

  return { prepareChatImage, MAX_DIMENSION, MAX_OUTPUT_CHARS };
});
