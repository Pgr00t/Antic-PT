import { describe, it, expect } from 'vitest';
import { applyPatch } from './reconciler';
import { SSEParser } from './parser';

describe('Reconciler - applyPatch', () => {
  it('should apply add operations', () => {
    const data = { a: 1 };
    const ops = [{ op: 'add', path: '/b', value: 2 }];
    expect(applyPatch(data, ops)).toEqual({ a: 1, b: 2 });
  });

  it('should apply replace operations', () => {
    const data = { a: 1 };
    const ops = [{ op: 'replace', path: '/a', value: 2 }];
    expect(applyPatch(data, ops)).toEqual({ a: 2 });
  });

  it('should apply remove operations', () => {
    const data = { a: 1, b: 2 };
    const ops = [{ op: 'remove', path: '/b' }];
    expect(applyPatch(data, ops)).toEqual({ a: 1 });
  });

  it('should handle nested paths', () => {
    const data = { user: { name: 'Alice' } };
    const ops = [{ op: 'replace', path: '/user/name', value: 'Bob' }];
    expect(applyPatch(data, ops)).toEqual({ user: { name: 'Bob' } });
  });
});

describe('SSEParser', () => {
  it('should parse simple events', () => {
    let result: any;
    const parser = new SSEParser((ev) => { result = ev; });
    parser.push('event: test\nid: 1\ndata: {"foo":"bar"}\n\n');
    expect(result).toEqual({
      event: 'test',
      id: '1',
      data: '{"foo":"bar"}'
    });
  });

  it('should handle multiple chunks', () => {
    let result: any;
    const parser = new SSEParser((ev) => { result = ev; });
    parser.push('event: test\n');
    parser.push('id: 1\n');
    parser.push('data: {"foo":');
    parser.push('"bar"}\n\n');
    expect(result).toEqual({
      event: 'test',
      id: '1',
      data: '{"foo":"bar"}'
    });
  });
});
