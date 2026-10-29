// Orthogonal signal paths around an octagonal core distinguish Rune from
// the ordinary chat's organic shoal without adding an image or a new colour.
export function runeIntensity(x: number, y: number, cols: number, rows: number, seconds: number): number {
  const dx = (x - cols / 2) * 9;
  const dy = (y - rows / 2) * 14;
  const radius = Math.max(90, Math.min(cols * 9, rows * 14) * 0.38);
  const distance = (Math.max(Math.abs(dx), Math.abs(dy)) * 0.58 + (Math.abs(dx) + Math.abs(dy)) * 0.42) / radius;
  const ring = Math.min(Math.abs(distance - 0.86), Math.abs(distance - 1.3));
  let value = Math.max(0, 1 - ring * 22) * (0.28 + 0.12 * Math.sin(seconds * 0.8 + distance * 5));
  if (y % 6 === 0) {
    const pulse = (seconds * 7 + y * 3.7) % (cols + 16) - 8;
    value += 0.06 + Math.max(0, 1 - Math.abs(x - pulse) / 5) * 0.65;
  }
  if (x % 12 === 0) {
    const pulse = (seconds * 4 + x * 1.9) % (rows + 12) - 6;
    value += 0.06 + Math.max(0, 1 - Math.abs(y - pulse) / 4) * 0.55;
    if (y % 6 === 0) value += 0.18 + 0.1 * Math.sin(seconds + x * 0.3);
  }
  return Math.min(1, value);
}
