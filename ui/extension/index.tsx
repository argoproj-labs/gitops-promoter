import AppViewExtension from "./AppViewExtension";
import type { Application } from "@shared/types/extension";

const hasOnePromotionStrategy = (application: Application) => {
  const promotionStrategyResources = application.status?.resources?.filter(
    (resource) => resource.kind === "PromotionStrategy"
  );
  return (promotionStrategyResources?.length || 0) === 1;
};

// Register app view extension
window.extensionsAPI?.registerAppViewExtension(
  AppViewExtension,
  "PromotionStrategy",
  "fa-code-branch",
  hasOnePromotionStrategy
);
