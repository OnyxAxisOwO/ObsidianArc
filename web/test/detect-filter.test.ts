import { describe, expect, it } from 'vitest';
import { filterDetected } from '@/lib/detect-filter';

const entries = [
  { model_id: 'anthropic/claude-opus-5', display_name: '' },
  { model_id: 'openai/gpt-5', display_name: 'GPT Five' },
  { model_id: 'qwen/qwen3', display_name: '' },
];

describe('filterDetected', () => {
  it('returns everything for a blank query', () => {
    expect(filterDetected(entries, '  ')).toHaveLength(3);
  });

  it('matches the id or the display name, ignoring case', () => {
    expect(filterDetected(entries, 'CLAUDE').map((e) => e.model_id)).toEqual(['anthropic/claude-opus-5']);
    expect(filterDetected(entries, 'five').map((e) => e.model_id)).toEqual(['openai/gpt-5']);
  });

  it('returns nothing when nothing matches', () => {
    expect(filterDetected(entries, 'zzz')).toEqual([]);
  });
});
