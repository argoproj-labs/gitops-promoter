import '@lib/styles/main.scss';

import React, { StrictMode } from 'react';
import { createRoot } from 'react-dom/client';
import App from './App';

// Published before any plugin script can load, so an externally loaded plugin
// bundle that externalizes `react` resolves to this same instance rather than
// bundling a second copy of React.
window.React = React;

const rootEl = document.getElementById('root');
if (rootEl) {
  createRoot(rootEl).render(
    <StrictMode>
      <App />
    </StrictMode>,
  );
}
