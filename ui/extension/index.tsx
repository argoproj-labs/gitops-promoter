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
