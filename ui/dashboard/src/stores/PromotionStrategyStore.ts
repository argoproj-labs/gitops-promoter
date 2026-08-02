import { createCRDStore, type PromotionStrategyItem } from './CRDStore';

export const PromotionStrategyStore = createCRDStore<PromotionStrategyItem>(
  'PromotionStrategyDetails',
  'PromotionStrategyDetails',
);
