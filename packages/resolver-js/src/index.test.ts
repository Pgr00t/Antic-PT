/// <reference types="vitest/globals" />
/**
 * Antic-PT v0.2 SDK — Unit Tests
 *
 * Tests the pure logic components: header parsing, applyPatch,
 * and protocol state transitions via a mock fetch.
 */

import { AnticipationResolver, PatchOp } from './resolver';

// ── Header parsing (via exposed static helpers) ──────────────────────────────

describe('AnticipationResolver.applyPatch', () => {
  it('applies replace op to top-level field', () => {
    const ops: PatchOp[] = [{ op: 'replace', path: '/cpuUsage', value: 0.81 }];
    const result = AnticipationResolver.applyPatch({ cpuUsage: 0.73, memoryUsage: 0.61 }, ops);
    expect(result.cpuUsage).toBe(0.81);
    expect(result.memoryUsage).toBe(0.61);
  });

  it('applies add op', () => {
    const ops: PatchOp[] = [{ op: 'add', path: '/newField', value: 42 }];
    const result = AnticipationResolver.applyPatch({ existing: 1 }, ops);
    expect(result.newField).toBe(42);
    expect(result.existing).toBe(1);
  });

  it('applies remove op', () => {
    const ops: PatchOp[] = [{ op: 'remove', path: '/toDelete' }];
    const result = AnticipationResolver.applyPatch({ toDelete: 'gone', keep: 'yes' }, ops);
    expect(result.toDelete).toBeUndefined();
    expect(result.keep).toBe('yes');
  });

  it('applies multiple ops in sequence', () => {
    const ops: PatchOp[] = [
      { op: 'replace', path: '/a', value: 10 },
      { op: 'replace', path: '/b', value: 20 },
    ];
    const result = AnticipationResolver.applyPatch({ a: 1, b: 2, c: 3 }, ops);
    expect(result).toEqual({ a: 10, b: 20, c: 3 });
  });

  it('does not mutate the original object', () => {
    const original = { x: 1 };
    const ops: PatchOp[] = [{ op: 'replace', path: '/x', value: 99 }];
    AnticipationResolver.applyPatch(original, ops);
    expect(original.x).toBe(1);
  });
});

// ── Status lifecycle ──────────────────────────────────────────────────────────

describe('AnticipationResolver status', () => {
  it('starts as idle', () => {
    const resolver = new AnticipationResolver('/spec/user/1');
    expect(resolver.currentStatus).toBe('idle');
  });
});

// ── Confirmed response (no Reconcile-Id) ─────────────────────────────────────

describe('AnticipationResolver confirmed path', () => {
  it('emits speculative then confirm when X-Antic-State is confirmed', async () => {
    const capturedEvents: string[] = [];
    let capturedData: any = null;

    const mockFetch = (_url: string, _opts: any) =>
      Promise.resolve({
        ok: true,
        headers: { get: (h: string) => (h === 'X-Antic-State' ? 'confirmed' : null) },
        json: () => Promise.resolve({ name: 'Alice', role: 'Designer' }),
      } as any);

    const resolver = new AnticipationResolver('/spec/user/1', {});
    (resolver as any).baseUrl = '';
    // Patch global fetch
    const orig = globalThis.fetch;
    (globalThis as any).fetch = mockFetch;

    resolver
      .on('speculative', (data) => { capturedEvents.push('speculative'); capturedData = data; })
      .on('confirm', () => capturedEvents.push('confirm'));

    await resolver.fetch();

    expect(capturedEvents).toEqual(['speculative', 'confirm']);
    expect(capturedData.name).toBe('Alice');
    expect(resolver.currentStatus).toBe('confirmed');

    (globalThis as any).fetch = orig;
  });
});
