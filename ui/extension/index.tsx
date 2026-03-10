import AppViewExtension from "./AppViewExtension";
import { hasOnePromotionStrategy } from "./hasOnePromotionStrategy";

// Register app view extension
window.extensionsAPI?.registerAppViewExtension(
  AppViewExtension,
  "PromotionStrategy",
  "fa-code-branch",
  hasOnePromotionStrategy,
);
