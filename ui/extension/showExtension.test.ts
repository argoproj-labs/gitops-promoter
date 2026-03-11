import { describe, it, expect } from 'vitest';
import { showExtension, ANNOTATION } from './showExtension';
import type { Application } from '@shared/types/extension';

const makeApp = (overrides: Partial<Application> = {}): Application => ({
  metadata: { name: 'my-app', namespace: 'default' },
  ...overrides,
});

describe('showExtension', () => {
  describe('annotation-based lookup', () => {
    it('returns true when the annotation is present', () => {
      const app = makeApp({
        metadata: {
          name: 'my-app',
          namespace: 'default',
          annotations: { [ANNOTATION]: 'true' },
        },
      });
      expect(showExtension(app)).toBe(true);
    });

    it('returns true when annotation is present and there are no tree resources', () => {
      const app = makeApp({
        metadata: {
          name: 'my-app',
          namespace: 'default',
          annotations: { [ANNOTATION]: 'true' },
        },
        status: { resources: [] },
      });
      expect(showExtension(app)).toBe(true);
    });

    it('returns true when annotation is present even with multiple PromotionStrategy resources', () => {
      const app = makeApp({
        metadata: {
          name: 'my-app',
          namespace: 'default',
          annotations: { [ANNOTATION]: 'true' },
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

  describe('tree-based fallback (no annotation)', () => {
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

    it('returns false when annotations object is empty', () => {
      const app = makeApp({
        metadata: { name: 'my-app', namespace: 'default', annotations: {} },
        status: { resources: [] },
      });
      expect(showExtension(app)).toBe(false);
    });
  });
});
