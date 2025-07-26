import type { PromotionStrategy } from '@shared/utils/PSData';
import { createCRDStore } from './CRDStore';

export const PromotionStrategyStore = createCRDStore<PromotionStrategy>('PromotionStrategy', 'PromotionStrategy') 