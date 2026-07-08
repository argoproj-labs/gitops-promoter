import { describe, it, expect } from 'vitest';
import { healthFromStatuses, shortSha, commitKey } from './helpers';
import type { Commit, CommitStatus } from '@shared/types/promotion';

const status = (phase: string, key = phase): CommitStatus => ({ key, phase });

describe('healthFromStatuses', () => {
  it('returns "unknown" for undefined', () => {
    expect(healthFromStatuses(undefined)).toBe('unknown');
  });

  it('returns "unknown" for an empty array', () => {
    expect(healthFromStatuses([])).toBe('unknown');
  });

  it('returns "failure" when any status has failed', () => {
    expect(healthFromStatuses([status('success'), status('failure')])).toBe('failure');
  });

  it('prioritizes "failure" over "pending"', () => {
    expect(healthFromStatuses([status('pending'), status('failure')])).toBe('failure');
  });

  it('returns "pending" when any status is pending and none failed', () => {
    expect(healthFromStatuses([status('success'), status('pending')])).toBe('pending');
  });

  it('returns "success" only when every status succeeded', () => {
    expect(healthFromStatuses([status('success'), status('success')])).toBe('success');
  });

  it('returns "unknown" when statuses are neither failing, pending, nor all successful', () => {
    // e.g. an unrecognized phase mixed in with successes
    expect(healthFromStatuses([status('success'), status('running')])).toBe('unknown');
  });

  it('returns "unknown" for a single unrecognized phase', () => {
    expect(healthFromStatuses([status('running')])).toBe('unknown');
  });
});

describe('shortSha', () => {
  it('returns "" for undefined', () => {
    expect(shortSha(undefined)).toBe('');
  });

  it('returns "" for an empty string', () => {
    expect(shortSha('')).toBe('');
  });

  it('truncates a full sha to 7 characters', () => {
    expect(shortSha('abcdef1234567890')).toBe('abcdef1');
  });

  it('leaves a sha shorter than 7 characters unchanged', () => {
    expect(shortSha('abc')).toBe('abc');
  });

  it('returns a 7-char sha unchanged', () => {
    expect(shortSha('abcdef1')).toBe('abcdef1');
  });
});

describe('commitKey', () => {
  it('returns null for undefined', () => {
    expect(commitKey(undefined)).toBeNull();
  });

  it('returns the short sha when sha is at least 7 characters', () => {
    expect(commitKey({ sha: 'abcdef1234567890' } as Commit)).toBe('abcdef1');
  });

  it('uses the sha when it is exactly 7 characters', () => {
    expect(commitKey({ sha: 'abcdef1' } as Commit)).toBe('abcdef1');
  });

  it('falls back to a nokey when sha is shorter than 7 characters', () => {
    expect(commitKey({ sha: 'abc', subject: 'fix', author: 'jane' } as Commit)).toBe(
      'nokey:fix|jane',
    );
  });

  it('builds a nokey from subject and author when no usable sha', () => {
    expect(commitKey({ subject: 'add feature', author: 'joe' } as Commit)).toBe(
      'nokey:add feature|joe',
    );
  });

  it('builds a nokey from subject alone', () => {
    expect(commitKey({ subject: 'add feature' } as Commit)).toBe('nokey:add feature|');
  });

  it('builds a nokey from author alone', () => {
    expect(commitKey({ author: 'joe' } as Commit)).toBe('nokey:|joe');
  });

  it('returns null when there is no sha, subject, or author', () => {
    expect(commitKey({} as Commit)).toBeNull();
  });

  it('returns null when the commit has only a body', () => {
    expect(commitKey({ body: 'details' } as Commit)).toBeNull();
  });
});
