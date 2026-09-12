import type { Application } from '@shared/types/extension';

export const LABEL = 'promoter.argoproj.io/has-promotionstrategy';

const GROUP = 'view.promoter.argoproj.io';
const KIND = 'PromotionStrategyDetails';

export const showExtension = (application: Application): boolean => {
  if (application.metadata.labels?.[LABEL]) return application.metadata.labels[LABEL] === 'true';
  const resources = application.status?.resources?.filter(
    (r) => r.kind === KIND && r.group === GROUP,
  );
  return (resources?.length || 0) >= 1;
};
