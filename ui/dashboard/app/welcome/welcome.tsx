import logoDark from "./logo-dark.svg";
import logoLight from "./logo-light.svg";
import {MyCounter} from '../../../component-lib/src/components/MyCounter';
import {
  PromotionStrategy,
} from "../../../component-lib/src/components/TestPromotionStrategy";
import {useCallback, useEffect} from "react";
import type {PromotionStrategyType} from "../../../component-lib/src/models/promotionstrategy";

import { usePromotionStrategyStore } from '~/stores/store';


export function Welcome() {

  // Use selectors for reactive state
  const strategies = usePromotionStrategyStore(state => state.strategies);
  const loading = usePromotionStrategyStore(state => state.loading);
  const error = usePromotionStrategyStore(state => state.error);

  console.log('Rendering with strategies:', strategies);

  useEffect(() => {
  }, []);

  if (loading) return <div>Loading...</div>;
  if (error) return <div>{error}</div>;

  return (
      <main className="flex items-center justify-center pt-16 pb-4">
        <div className="flex-1 flex flex-col items-center gap-16 min-h-0">
          <header className="flex flex-col items-center gap-9">
          </header>
          {strategies.length === 0 ? (
              <div>No strategies found</div>
          ) : (
              <>
                {strategies.map((strategy: PromotionStrategyType) => (
                    <PromotionStrategy key={strategy.metadata.name} ps={strategy} />
                ))}
              </>
          )}
        </div>
      </main>
  );
}