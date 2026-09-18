import { describe, it, expect } from 'vitest';
import { sameUrlState, initialUrlState } from '@components-lib/components/HistoryView/urlState';

const DEV_BRANCH = 'environments/dev';
const PRD_BRANCH = 'environments/prd';

describe('sameUrlState', () => {
  it('treats an identical envFilter order as the same state', () => {
    const a = initialUrlState(null, { envFilter: [DEV_BRANCH, PRD_BRANCH] });
    const b = initialUrlState(null, { envFilter: [DEV_BRANCH, PRD_BRANCH] });

    expect(sameUrlState(a, b)).toBe(true);
  });

  it('treats a different envFilter order as a distinct state', () => {
    const a = initialUrlState(null, { envFilter: [DEV_BRANCH, PRD_BRANCH] });
    const b = initialUrlState(null, { envFilter: [PRD_BRANCH, DEV_BRANCH] });

    expect(sameUrlState(a, b)).toBe(false);
  });
});
