import { readFileSync } from 'node:fs';
import { resolve } from 'node:path';
import { describe, expect, it } from 'vitest';

// Compares the interfaces in contracts.ts / reportsClient.ts with backend/testdata/contract_fields.json,
// which backend/internal/contract asserts against the real Go structs. cwd = frontend/.
const goFields: Record<string, string[]> = JSON.parse(
  readFileSync(resolve(process.cwd(), '../backend/testdata/contract_fields.json'), 'utf8'),
);
const sources = ['src/contracts.ts', 'src/reportsClient.ts'].map((f) => readFileSync(resolve(process.cwd(), f), 'utf8')).join('\n');

/** Own + inherited member names of `export interface Name [extends Base] { ... }` (no nested object types). */
function members(name: string, seen = new Set<string>()): string[] | null {
  const m = new RegExp(`export interface ${name}(?:\\s+extends\\s+(\\w+))?\\s*\\{([^}]*)\\}`).exec(sources);
  if (!m || seen.has(name)) return null;
  seen.add(name);
  const own = (m[2] ?? '')
    .split(/[;\n]/)
    .map((p) => /^\s*(\w+)\??\s*:/.exec(p)?.[1])
    .filter((x): x is string => !!x);
  const base = m[1] ? members(m[1], seen) ?? [] : [];
  return [...base, ...own];
}

describe('TypeScript interfaces mirror the Go API structs', () => {
  it('has a contract entry for every mirrored type', () => {
    expect(Object.keys(goFields).length).toBeGreaterThanOrEqual(15);
  });

  for (const [name, fields] of Object.entries(goFields)) {
    it(`${name}`, () => {
      const ts = members(name);
      expect(ts, `interface ${name} not found in contracts.ts`).not.toBeNull();
      expect([...(ts ?? [])].sort()).toEqual(fields);
    });
  }

  it('the extractor itself understands extends and optional members', () => {
    expect(members('CASReport')).toEqual(expect.arrayContaining(['funds', 'pairs', 'source', 'warnings', 'unmatchedIsins']));
    expect(members('PairAction')).toContain('holdUntil'); // optional (holdUntil?: string)
  });
});
