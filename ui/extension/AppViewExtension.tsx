import React, { useEffect, useMemo, useState } from "react";
import Card from "@components-lib/components/Card";
import { PromotionStrategy } from "@shared/types/promotion";
import { AppViewComponentProps } from "@shared/types/extension";

const ANNOTATION = "promoter.argoproj.io/promotion-strategy";
const GROUP = "promoter.argoproj.io";
const KIND = "PromotionStrategy";

const AppViewExtension = ({ tree, application }: AppViewComponentProps) => {
  const promotionStrategyNodes = useMemo(
    () => tree.nodes.filter((node) => node.kind === KIND),
    [tree.nodes],
  );
  const [promotionStrategy, setPromotionStrategy] = useState<PromotionStrategy>();
  const [fetchError, setFetchError] = useState<string | null>(null);

  const annotationValue = application.metadata.annotations?.[ANNOTATION];

  const fetchParams = useMemo(() => {
    if (annotationValue) {
      const [namespace, name] = annotationValue.split("/");
      return { name, namespace, version: "v1alpha1" };
    }
    if (promotionStrategyNodes.length === 1) {
      const node = promotionStrategyNodes[0];
      return { name: node.name, namespace: node.namespace, version: node.version };
    }
    return null;
  }, [annotationValue, promotionStrategyNodes]);

  useEffect(() => {
    if (!fetchParams) return;
    const { name, namespace, version } = fetchParams;
    const url = `/api/v1/applications/${application.metadata.name}/resource?name=${name}&appNamespace=${application.metadata.namespace}&namespace=${namespace}&resourceName=${name}&version=${version}&kind=${KIND}&group=${GROUP}`;

    setFetchError(null);
    fetch(url)
      .then((response) => response.json())
      .then((data) => {
        setPromotionStrategy(
          typeof data.manifest === "string" ? JSON.parse(data.manifest) : data.manifest,
        );
      })
      .catch((err) => {
        console.error("Error fetching promotion strategy data: " + err);
        setFetchError("Failed to load PromotionStrategy: " + err);
      });
  }, [fetchParams, application.metadata.name, application.metadata.namespace]);

  if (!annotationValue) {
    if (promotionStrategyNodes.length === 0) {
      return <div>No PromotionStrategy resource found for this application.</div>;
    }
    if (promotionStrategyNodes.length > 1) {
      return (
        <div>
          Expected exactly one PromotionStrategy resource, found {promotionStrategyNodes.length}.
        </div>
      );
    }
  }
  if (!promotionStrategy) {
    if (fetchError) {
      return <div>{fetchError}</div>;
    }
    return <div>Loading...</div>;
  }
  return (
    <div className="extension-container">
      <Card environments={promotionStrategy.status?.environments || []} />
    </div>
  );
};

export default AppViewExtension;
