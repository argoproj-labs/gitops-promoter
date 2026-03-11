import AppViewExtension from './AppViewExtension';
import { showExtension } from './showExtension';

// Register app view extension
window.extensionsAPI?.registerAppViewExtension(
  AppViewExtension,
  'PromotionStrategy',
  'fa-code-branch',
  showExtension,
);
