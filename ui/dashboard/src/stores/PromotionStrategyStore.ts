import { createCRDStore } from './CRDStore';
import type { PromotionStrategyType } from '@shared/models/PromotionStrategyType';

export const PromotionStrategyStore = createCRDStore<PromotionStrategyType>('PromotionStrategy', 'PromotionStrategy') 