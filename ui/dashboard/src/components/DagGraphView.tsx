import React, { useId, useLayoutEffect, useMemo, useRef, useState } from 'react';
import Card from '@lib/components/Card';
import type { Environment } from '@shared/types/promotion';
import { computeTopologyDepths, type TopologyNode } from '@shared/utils/dagTopology';
import './DagGraphView.scss';

interface DagGraphViewProps {
  topology: TopologyNode[];
  environments: Environment[];
}

const DagGraphView: React.FC<DagGraphViewProps> = ({ topology, environments }) => {
  const markerId = `dag-arrowhead-${useId().replaceAll(':', '')}`;
  const containerRef = useRef<HTMLDivElement>(null);
  const nodeRefs = useRef<Map<string, HTMLDivElement>>(new Map());
  const [edgePaths, setEdgePaths] = useState<string[]>([]);

  const { rows, edges } = useMemo(() => {
    const depths = computeTopologyDepths(topology);
    const maxDepth = Math.max(0, ...depths.values());
    const topologyRows: string[][] = Array.from({ length: maxDepth + 1 }, () => []);

    for (const node of topology) {
      topologyRows[depths.get(node.branch) ?? 0].push(node.branch);
    }

    return {
      rows: topologyRows,
      edges: topology.flatMap((node) => node.dependsOn.map((from) => ({ from, to: node.branch }))),
    };
  }, [topology]);

  useLayoutEffect(() => {
    if (!containerRef.current) return;

    const measure = () => {
      const container = containerRef.current;
      if (!container) return;

      const base = container.getBoundingClientRect();
      const rect = (branch: string) => {
        const element = nodeRefs.current.get(branch);
        if (!element) return null;
        const nodeRect = element.getBoundingClientRect();
        return {
          left: nodeRect.left - base.left,
          right: nodeRect.right - base.left,
          top: nodeRect.top - base.top,
          bottom: nodeRect.bottom - base.top,
        };
      };

      const incomingCount: Record<string, number> = {};
      const outgoingCount: Record<string, number> = {};
      for (const edge of edges) {
        incomingCount[edge.to] = (incomingCount[edge.to] ?? 0) + 1;
        outgoingCount[edge.from] = (outgoingCount[edge.from] ?? 0) + 1;
      }

      const incomingIndex: Record<string, number> = {};
      const outgoingIndex: Record<string, number> = {};
      const attachment = (left: number, right: number, index: number, count: number) => {
        const width = right - left;
        if (count <= 1) return left + width / 2;
        const span = width * 0.6;
        return left + (width - span) / 2 + (span * index) / (count - 1);
      };

      const nextPaths: string[] = [];
      for (const edge of edges) {
        const source = rect(edge.from);
        const target = rect(edge.to);
        if (!source || !target) continue;

        const sourceIndex = outgoingIndex[edge.from] ?? 0;
        const targetIndex = incomingIndex[edge.to] ?? 0;
        outgoingIndex[edge.from] = sourceIndex + 1;
        incomingIndex[edge.to] = targetIndex + 1;

        const sourceX = attachment(
          source.left,
          source.right,
          sourceIndex,
          outgoingCount[edge.from],
        );
        const targetX = attachment(target.left, target.right, targetIndex, incomingCount[edge.to]);

        nextPaths.push(`M ${sourceX} ${source.bottom} L ${targetX} ${target.top - 10}`);
      }

      setEdgePaths(nextPaths);
    };

    measure();
    window.addEventListener('resize', measure);
    const animationFrame = window.requestAnimationFrame(measure);

    return () => {
      window.removeEventListener('resize', measure);
      window.cancelAnimationFrame(animationFrame);
    };
  }, [edges, environments]);

  const environmentsByBranch = new Map(
    environments.map((environment) => [environment.branch, environment]),
  );

  return (
    <div className="dag-graph" ref={containerRef}>
      <svg className="dag-graph__edges" aria-hidden="true">
        <defs>
          <marker
            id={markerId}
            markerWidth="12"
            markerHeight="12"
            refX="9"
            refY="5"
            orient="auto"
            markerUnits="userSpaceOnUse"
          >
            <path d="M1,1 L10,5 L1,9 Z" className="dag-graph__arrowhead" />
          </marker>
        </defs>
        {edgePaths.map((path, index) => (
          <path
            key={`${edges[index]?.from}-${edges[index]?.to}`}
            d={path}
            className="dag-graph__edge"
            markerEnd={`url(#${markerId})`}
          />
        ))}
      </svg>

      {rows.map((row, depth) => (
        <div className="dag-graph__row" key={depth}>
          {row.map((branch) => {
            const environment = environmentsByBranch.get(branch);
            return (
              <div
                className="dag-graph__node"
                key={branch}
                ref={(element) => {
                  if (element) nodeRefs.current.set(branch, element);
                  else nodeRefs.current.delete(branch);
                }}
              >
                {environment ? (
                  <Card environments={[environment]} />
                ) : (
                  <div className="dag-graph__node-missing">
                    {branch}
                    <small>No live status</small>
                  </div>
                )}
              </div>
            );
          })}
        </div>
      ))}
    </div>
  );
};

export default DagGraphView;
