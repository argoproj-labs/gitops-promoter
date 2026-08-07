import React, { useMemo, useState } from 'react';
import Card from '@lib/components/Card';
import { type PromotionStrategy } from '@shared/utils/PSData';
import type { PromotionStrategyDetails } from '@shared/types/view';
import { resolvePromotionTopologies, type PromotionTopology } from '@shared/utils/dagTopology';
import DagGraphView from './DagGraphView';
import './PromotionStrategyDetailsView.scss';

interface PromotionStrategyDetailsViewProps {
  strategy: PromotionStrategy;
  details: PromotionStrategyDetails;
}

export const PromotionStrategyDetailsView: React.FC<PromotionStrategyDetailsViewProps> = ({
  strategy,
  details,
}) => {
  const [selectedTopologyId, setSelectedTopologyId] = useState('');
  const [layoutOverride, setLayoutOverride] = useState<'linear' | 'graph' | null>(null);
  const environments = strategy.status?.environments || [];
  const topologies = useMemo(() => resolvePromotionTopologies(details), [details]);
  const topologyId = (topology: PromotionTopology) =>
    `${topology.source.kind}/${topology.source.name}/${topology.key}`;
  const selectedTopology =
    topologies.find((topology) => topologyId(topology) === selectedTopologyId) ?? topologies.at(0);
  const graphTopology =
    selectedTopology &&
    (layoutOverride === 'graph' || (layoutOverride === null && selectedTopology.isChain === false))
      ? selectedTopology
      : undefined;
  const showGraph = graphTopology !== undefined;

  return (
    <div>
      {selectedTopology && (
        <div className="topology-controls">
          {topologies.length > 1 && (
            <label className="topology-controls__selector">
              Ordering gate
              <select
                value={topologyId(selectedTopology)}
                onChange={(event) => {
                  setSelectedTopologyId(event.target.value);
                  setLayoutOverride(null);
                }}
              >
                {topologies.map((topology) => (
                  <option key={topologyId(topology)} value={topologyId(topology)}>
                    {topology.key} ({topology.source.kind})
                  </option>
                ))}
              </select>
            </label>
          )}

          <div className="topology-controls__layout">
            <button
              className={`strategy-page-tab ${!showGraph ? 'active' : ''}`}
              onClick={() => setLayoutOverride('linear')}
            >
              Linear
            </button>
            <button
              className={`strategy-page-tab ${showGraph ? 'active' : ''}`}
              onClick={() => setLayoutOverride('graph')}
            >
              Graph
            </button>
          </div>

          {!selectedTopology.materialized && (
            <span className="topology-controls__pending">Preparing ordering topology…</span>
          )}
        </div>
      )}

      {graphTopology ? (
        <DagGraphView topology={graphTopology.nodes} environments={environments} />
      ) : (
        <Card environments={environments} />
      )}
    </div>
  );
};

export default PromotionStrategyDetailsView;
