import React from 'react';
import yaml from 'js-yaml';
import { Prism as SyntaxHighlighter } from 'react-syntax-highlighter';
import { oneDark } from 'react-syntax-highlighter/dist/esm/styles/prism';

// TypeScript interface for PromotionStrategy - simplified version
interface PromotionStrategy {
  metadata?: {
    name?: string;
    namespace?: string;
  };
  [key: string]: any;
}

// ArgoCD extension component interface
interface LiveManifestViewProps {
  application: {
    metadata: {
      name: string;
      namespace: string;
    };
  };
  resource: PromotionStrategy;
}

// Live Manifest View component
const LiveManifestView: React.FC<LiveManifestViewProps> = ({ resource }) => {
  const [yamlContent, setYamlContent] = React.useState('');

  React.useEffect(() => {
    try {
      // Convert the PromotionStrategy resource to YAML
      const yamlString = yaml.dump(resource, { 
        indent: 2,
        lineWidth: -1,
        noRefs: true,
        sortKeys: false
      });
      setYamlContent(yamlString);
    } catch (error) {
      setYamlContent('# Error converting resource to YAML\n# ' + String(error));
    }
  }, [resource]);

  return (
    <div style={{ height: '100%', overflow: 'auto' }}>
      <div style={{ 
        padding: '10px', 
        borderBottom: '1px solid #e1e5e9',
        backgroundColor: '#f8f9fa',
        fontSize: '14px',
        fontWeight: 'bold'
      }}>
        Live Manifest (YAML)
      </div>
      <SyntaxHighlighter
        language="yaml"
        style={oneDark}
        showLineNumbers={true}
        wrapLines={true}
        customStyle={{
          margin: 0,
          fontSize: '12px',
          fontFamily: 'Monaco, Menlo, "Ubuntu Mono", monospace',
          maxHeight: 'calc(100vh - 200px)',
          overflow: 'auto'
        }}
        codeTagProps={{
          style: {
            fontFamily: 'Monaco, Menlo, "Ubuntu Mono", monospace'
          }
        }}
      >
        {yamlContent}
      </SyntaxHighlighter>
    </div>
  );
};

export default LiveManifestView;