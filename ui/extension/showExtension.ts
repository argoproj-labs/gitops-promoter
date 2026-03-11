import type { Application } from '@shared/types/extension';

export const LABEL = 'promoter.argoproj.io/extension-enabled';

export const showExtension = (application: Application): boolean => {
  if (application.metadata.labels?.[LABEL]) return true;
  const resources = application.status?.resources?.filter(
    (r) => r.kind === 'PromotionStrategy' && r.group === 'promoter.argoproj.io',
  );
  return (resources?.length || 0) >= 1;
};
