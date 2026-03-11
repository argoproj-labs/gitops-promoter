import { describe, it, expect } from 'vitest';
import { showExtension, LABEL } from './showExtension';
import type { Application } from '@shared/types/extension';

const makeApp = (overrides: Partial<Application> = {}): Application => ({
  metadata: { name: 'my-app', namespace: 'default' },
  ...overrides,
});

describe('showExtension', () => {
  describe('label-based lookup', () => {
    it('returns true when the label is present', () => {
      const app = makeApp({
        metadata: {
          name: 'my-app',
          namespace: 'default',
          labels: { [LABEL]: 'true' },
        },
      });
      expect(showExtension(app)).toBe(true);
    });

    it('returns true when label is present and there are no tree resources', () => {
      const app = makeApp({
        metadata: {
          name: 'my-app',
          namespace: 'default',
          labels: { [LABEL]: 'true' },
        },
        status: { resources: [] },
      });
      expect(showExtension(app)).toBe(true);
    });

    it('returns true when label is present even with multiple PromotionStrategy resources', () => {
      const app = makeApp({
        metadata: {
          name: 'my-app',
          namespace: 'default',
          labels: { [LABEL]: 'true' },
        },
        status: {
          resources: [
            {
              kind: 'PromotionStrategy',
              group: 'promoter.argoproj.io',
              name: 'ps1',
              namespace: 'ns1',
              status: 'Synced',
            },
            {
              kind: 'PromotionStrategy',
              group: 'promoter.argoproj.io',
              name: 'ps2',
              namespace: 'ns2',
              status: 'Synced',
            },
          ],
        },
      });
      expect(showExtension(app)).toBe(true);
    });
  });

  describe('tree-based fallback (no label)', () => {
    it('returns true when exactly one PromotionStrategy resource is in the tree', () => {
      const app = makeApp({
        status: {
          resources: [
            {
              kind: 'PromotionStrategy',
              group: 'promoter.argoproj.io',
              name: 'ps1',
              namespace: 'ns1',
              status: 'Synced',
            },
          ],
        },
      });
      expect(showExtension(app)).toBe(true);
    });

    it('returns true when there are multiple PromotionStrategy resources', () => {
      const app = makeApp({
        status: {
          resources: [
            {
              kind: 'PromotionStrategy',
              group: 'promoter.argoproj.io',
              name: 'ps1',
              namespace: 'ns1',
              status: 'Synced',
            },
            {
              kind: 'PromotionStrategy',
              group: 'promoter.argoproj.io',
              name: 'ps2',
              namespace: 'ns2',
              status: 'Synced',
            },
          ],
        },
      });
      expect(showExtension(app)).toBe(true);
    });

    it('returns false when there are no resources', () => {
      const app = makeApp({ status: { resources: [] } });
      expect(showExtension(app)).toBe(false);
    });

    it('returns false when status is absent', () => {
      const app = makeApp();
      expect(showExtension(app)).toBe(false);
    });

    it('returns false when there are no PromotionStrategy resources (only other kinds)', () => {
      const app = makeApp({
        status: {
          resources: [
            {
              kind: 'Deployment',
              group: 'apps',
              name: 'deploy',
              namespace: 'ns1',
              status: 'Synced',
            },
          ],
        },
      });
      expect(showExtension(app)).toBe(false);
    });

    it('returns false when PromotionStrategy has wrong group', () => {
      const app = makeApp({
        status: {
          resources: [
            {
              kind: 'PromotionStrategy',
              group: 'other.io',
              name: 'ps1',
              namespace: 'ns1',
              status: 'Synced',
            },
          ],
        },
      });
      expect(showExtension(app)).toBe(false);
    });

    it('returns false when labels object is empty', () => {
      const app = makeApp({
        metadata: { name: 'my-app', namespace: 'default', labels: {} },
        status: { resources: [] },
      });
      expect(showExtension(app)).toBe(false);
    });
  });
});
