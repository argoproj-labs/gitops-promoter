import React from 'react';
import { loadPluginBundle } from '@shared/components/plugins';
import AppViewExtension from './AppViewExtension';
import { injectIconStyles } from './injectIconStyles';
import { showExtension } from './showExtension';

// Argo CD only supports FontAwesome icon class names for app view extensions.
// We use a custom class and inject our icon styles into the document so the tab
// shows the GitOps Promoter logo without requiring operator-configured CSS.
const APP_VIEW_ICON_CLASS = 'gitops-promoter-app-view-icon';

injectIconStyles();

window.extensionsAPI?.registerAppViewExtension(
  AppViewExtension,
  'GitOps Promoter',
  APP_VIEW_ICON_CLASS,
  showExtension,
);

// This extension is installed into the argocd-server pod and served from
// Argo CD's own origin; every existing call in this package (AppViewExtension's
// resource fetches) goes through Argo CD's `/api/v1/applications/.../resource`
// proxy rather than any same-origin promoter route. There is no established
// path from here to the promoter webserver's own `/plugins.js`, so this is a
// best-effort same-origin fetch that only works when the promoter is reverse
// proxied under the same origin as argocd-server. Until that's set up, this
// will 404 harmlessly (handled by `loadPluginBundle`'s onerror).
loadPluginBundle(React);
