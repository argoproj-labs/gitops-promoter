import type { Application } from "@shared/types/extension";

export const ANNOTATION = "promoter.argoproj.io/promotion-strategy";

export const hasOnePromotionStrategy = (application: Application): boolean => {
  if (application.metadata.annotations?.[ANNOTATION]) return true;
  const resources = application.status?.resources?.filter((r) => r.kind === "PromotionStrategy");
  return (resources?.length || 0) === 1;
};
