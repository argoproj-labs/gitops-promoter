import React, { useState, useEffect } from 'react';
import AceEditor from 'react-ace';
import yaml from 'js-yaml';
import type { PromotionStrategy } from '@shared/utils/PSData';
import { useDarkTheme } from './useDarkTheme';
import './LiveManifestView.scss';

// Import ace editor modes and themes
import 'ace-builds/src-noconflict/mode-yaml';
import 'ace-builds/src-noconflict/theme-textmate';
import 'ace-builds/src-noconflict/theme-tomorrow_night';
import 'ace-builds/src-noconflict/ext-language_tools';

interface LiveManifestViewProps {
  strategy: PromotionStrategy;
}

export const LiveManifestView: React.FC<LiveManifestViewProps> = ({ strategy }) => {
  const [yamlString, setYamlString] = useState<string>('');
  const dark = useDarkTheme();

  useEffect(() => {
    const formatted = yaml.dump(strategy, {
      indent: 2,
      lineWidth: -1,
      quotingType: '"',
      forceQuotes: false,
    });
    setYamlString(formatted);
  }, [strategy]);

  return (
    <div className="strategy-json-editor">
      <div className="strategy-json-editor-content">
        <AceEditor
          mode="yaml"
          theme={dark ? 'tomorrow_night' : 'textmate'}
          value={yamlString}
          readOnly={true}
          showPrintMargin={false}
          showGutter={true}
          highlightActiveLine={false}
          fontSize={12}
          width="100%"
          height="100%"
          wrapEnabled={false}
          setOptions={{
            enableBasicAutocompletion: false,
            enableLiveAutocompletion: false,
            enableSnippets: false,
            showLineNumbers: true,
            tabSize: 2,
            useWorker: false,
            wrap: false,
            foldStyle: 'markbegin',
            showFoldWidgets: true,
            fadeFoldWidgets: false,
            behavioursEnabled: false,
            displayIndentGuides: false,
          }}
          editorProps={{
            $blockScrolling: Infinity,
          }}
          style={{
            fontFamily: 'Menlo, Monaco, monospace',
            lineHeight: '18px',
            border: 'none',
          }}
        />
      </div>
    </div>
  );
};
