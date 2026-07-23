import type { Rfc3339DateTime } from '../types/promotion';

const MINUTE = 60_000;
const HOUR = 60 * MINUTE;
const DAY = 24 * HOUR;

/**
 * RFC 3339 timestamp offset from "now" at call time. Offsets are stable across the
 * fixture; the absolute instants are recomputed on every load so relative renderings
 * (`timeAgo`, `TimeAgo`) always read fresh regardless of when the mock is opened.
 *
 * Positive values are in the past (e.g. `ago({ hours: 3 })` → three hours ago).
 */
export function ago(offset: { minutes?: number; hours?: number; days?: number }): Rfc3339DateTime {
  const ms = (offset.minutes ?? 0) * MINUTE + (offset.hours ?? 0) * HOUR + (offset.days ?? 0) * DAY;
  return new Date(Date.now() - ms).toISOString();
}
