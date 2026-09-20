import { installPluginHostApi } from '@shared/components/plugins';
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

// Plugin bundles from the shared plugins directory are concatenated onto this
// file at build time (see concat-plugins.mjs), so they run as part of this
// same script and self-register once it executes. The host API just needs to
// exist on `window` before that code runs.
installPluginHostApi();
