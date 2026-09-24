import '@lib/styles/main.scss';

import React, { StrictMode } from 'react';
import { createRoot } from 'react-dom/client';
import { loadPluginBundle } from './loadPluginBundle';
import App from './App';

// Published before any plugin script can load, so an externally loaded plugin
// bundle that externalizes `react` resolves to this same instance rather than
// bundling a second copy of React.
window.React = React;

// Rendering waits for the plugin bundle so every commit status row plugin is
// registered before the app's first render, rather than needing to pick up a
// late registration after the fact.
await loadPluginBundle(React);

const rootEl = document.getElementById('root');
if (rootEl) {
  createRoot(rootEl).render(
    <StrictMode>
      <App />
    </StrictMode>,
  );
}
