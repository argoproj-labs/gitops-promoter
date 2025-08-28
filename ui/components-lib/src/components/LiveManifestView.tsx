import React, { useState, useEffect } from 'react';
import yaml from 'js-yaml';
import type { PromotionStrategy } from '@shared/utils/PSData';
import './LiveManifestView.scss';

interface LiveManifestViewProps {
  strategy: PromotionStrategy;
}

export const LiveManifestView: React.FC<LiveManifestViewProps> = ({ strategy }) => {
  const [lines, setLines] = useState<string[]>([]);
  const [collapsedSections, setCollapsedSections] = useState<Set<number>>(new Set());

  // Convert to YAML
  useEffect(() => {
    if (strategy) {
      const formatted = yaml.dump(strategy, { indent: 2 });
      setLines(formatted.split('\n'));
    }
  }, [strategy]);

  // Make lines collapsible
  const isCollapsible = (index: number) => {
    const line = lines[index];
    if (!line?.trim().endsWith(':')) return false;
    
    // Check if next line has more indentation
    const nextLine = lines[index + 1];
    if (!nextLine) return false;
    
    const currentIndent = line.match(/^\s*/)?.[0].length || 0;
    const nextIndent = nextLine.match(/^\s*/)?.[0].length || 0;
    
    return nextIndent > currentIndent;
  };

  // Check if line should be hidden
  const isHidden = (index: number) => {
    const line = lines[index];
    let currentIndent = line.match(/^\s*/)?.[0].length || 0;
    
    
    for (let i = index - 1; i >= 0; i--) {
      const parentLine = lines[i];
      const parentIndent = parentLine.match(/^\s*/)?.[0].length || 0;
      
      if (parentIndent < currentIndent && parentLine.trim().endsWith(':')) {
        
        
        // If this parent is collapsed, hide the current line
        if (collapsedSections.has(i)) {
          return true;
        }
        currentIndent = parentIndent;
      }
    }
    return false;
  };

  const toggleCollapse = (index: number) => {
    setCollapsedSections(prev => {
      const newSet = new Set(prev);
      if (newSet.has(index)) {
        newSet.delete(index);
      } else {
        newSet.add(index);
      }
      return newSet;
    });
  };


  // Not Found
  if (!strategy) {
    return <div className="strategy-json-editor-empty">No strategy data available</div>;
  }

  return (
    <div className="strategy-json-editor">
      <div className="strategy-json-editor-content">
        <div className="line-numbers">
          {lines.map((_, index) => {
            if (isHidden(index)) return null;
            return (
              <div key={index} className="line-number">
                {index + 1}
                {isCollapsible(index) && (
                  <span 
                    className="collapse-icon"
                    onClick={(e) => {
                      e.stopPropagation();
                      toggleCollapse(index);
                    }}
                  >
                    {collapsedSections.has(index) ? '›' : '⌄'}
                  </span>
                )}
              </div>
            );
          })}
        </div>
        <div className="yaml-content">
          {lines.map((line, index) => {
            if (isHidden(index)) return null;
            
            const parts = line.match(/^(\s*)([^:]+):\s*(.*)$/);
            if (parts) {
              return (
                <div key={index} className="yaml-line">
                  <span className="yaml-indent">{parts[1]}</span>
                  <span className="yaml-key">{parts[2]}:</span>
                  <span className="yaml-value">{parts[3]}</span>
                </div>
              );
            }
            
            return <div key={index} className="yaml-line">{line}</div>;
          })}
        </div>
      </div>
    </div>
  );
}; 