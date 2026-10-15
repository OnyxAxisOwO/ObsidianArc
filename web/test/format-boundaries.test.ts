import { describe, expect, it } from 'vitest';
import { compactNumber, formatBytes, formatUptime } from '../src/lib/format';

// A figure that rounds up to the next unit must read as that unit, not as the
// number the unit below can no longer hold: 1000k and 60m are not sizes anyone
// would write down.
describe('unit boundaries in the figures', () => {
  it('moves a compact count that rounds to 1000k up to millions', () => {
    expect(compactNumber(999950)).toBe('1M');
    expect(compactNumber(999949)).toBe('999.9k');
    expect(compactNumber(1_000_000)).toBe('1M');
  });

  it('moves a compact count that rounds to 1000M up to billions', () => {
    expect(compactNumber(999_950_000)).toBe('1B');
    expect(compactNumber(999_949_999)).toBe('999.9M');
    expect(compactNumber(1_050_000_000)).toBe('1.1B');
  });

  it('moves a sub-thousand figure that rounds to 1000 up to thousands', () => {
    expect(compactNumber(999.999)).toBe('1k');
    expect(compactNumber(999.96)).toBe('999.96');
    expect(compactNumber(12.34)).toBe('12.34');
    expect(compactNumber(42)).toBe('42');
  });

  it('moves an uptime that rounds to 60 minutes up to hours', () => {
    expect(formatUptime(3570)).toBe('1h');
    expect(formatUptime(3599)).toBe('1h');
    expect(formatUptime(3000)).toBe('50m');
    expect(formatUptime(86399)).toBe('1d');
  });

  it('moves a size that rounds to 1024 MB up to gigabytes', () => {
    expect(formatBytes(1023.5 * 1024 * 1024)).toBe('1.0 GB');
    expect(formatBytes(1023.4 * 1024 * 1024)).toBe('1023 MB');
    expect(formatBytes(1024 * 1024 * 1024)).toBe('1.0 GB');
  });
});
