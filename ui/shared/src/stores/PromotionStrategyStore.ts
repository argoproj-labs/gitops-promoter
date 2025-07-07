import { createCRDStore } from './CRDStore';
import type { PromotionStrategyType } from '../models/PromotionStrategyType';

export const PromotionStrategyStore = createCRDStore<PromotionStrategyType>('PromotionStrategy', 'PromotionStrategy') 