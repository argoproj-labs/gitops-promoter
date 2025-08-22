import React, { useState, useEffect } from 'react';
import type { PromotionStrategy } from '@shared/utils/PSData';
import './LiveManifestView.scss';

interface LiveManifestViewProps {
  strategy: PromotionStrategy;
}

export const LiveManifestView: React.FC<LiveManifestViewProps> = ({ strategy }) => {
  const [lines, setLines] = useState<string[]>([]);


  // Convert to JSON
  useEffect(() => {
    if (strategy) {
      const formatted = JSON.stringify(strategy, null, 2);
      setLines(formatted.split('\n'));
    }
  }, [strategy]);


  // Not Found
  if (!strategy) {
    return <div className="strategy-json-editor-empty">No strategy data available</div>;
  }

  return (
    <div className="strategy-json-editor">
      <div className="strategy-json-editor-content">
        <div className="line-numbers">
          {lines.map((_, index) => (
            <div key={index} className="line-number">{index + 1}</div>
          ))}
        </div>
        <div className="json-content">
          {lines.map((line, index) => {



            // Split key and value in CRDs
            const parts = line.split(/^(\s*)"([^"]+)":\s/);
            if (parts.length > 2) {
              return (
                <div key={index} className="json-line">
                  {parts[1]}<span className="json-key">"{parts[2]}"</span>:
                  <span className="json-value">{parts[3]}</span>
                </div>
              );
            }
            
            return <div key={index} className="json-line">{line}</div>;
          })}
        </div>
      </div>
    </div>
  );
}; 