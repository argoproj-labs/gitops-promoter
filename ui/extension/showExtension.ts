import type { Application } from '@shared/types/extension';

export const ANNOTATION = 'promoter.argoproj.io/extension-enabled';

export const showExtension = (application: Application): boolean => {
  if (application.metadata.annotations?.[ANNOTATION]) return true;
  const resources = application.status?.resources?.filter(
    (r) => r.kind === 'PromotionStrategy' && r.group === 'promoter.argoproj.io',
  );
  return (resources?.length || 0) >= 1;
};
