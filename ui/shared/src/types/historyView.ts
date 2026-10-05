/** History view row filter (`all` shows every row). */
export const FILTER_IDS = ['all', 'live', 'in-flight', 'failed', 'no-op'] as const;
export type FilterId = (typeof FILTER_IDS)[number];

/** History view row ordering by commit time. */
export const SORT_IDS = ['newest', 'oldest'] as const;
export type SortId = (typeof SORT_IDS)[number];
