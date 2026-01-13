import React, { useEffect, useMemo, useState } from "react";
import Card from "@components-lib/components/Card";
import { PromotionStrategy } from "@shared/types/promotion";
import { AppViewComponentProps } from "@shared/types/extension";

const AppViewExtension = ({ tree, application }: AppViewComponentProps) => {
  const promotionStrategyNodes = useMemo(
    () => tree.nodes.filter((node) => node.kind === "PromotionStrategy"),
    [tree.nodes]
  );
  if (promotionStrategyNodes.length !== 1) {
    throw new Error(
      `Expected exactly one PromotionStrategy resource in Gitops Promoter Extension, found ${promotionStrategyNodes.length}`
    );
  }

  const [promotionStrategy, setPromotionStrategy] =
    useState<PromotionStrategy>();
  const [isLoading, setIsLoading] = useState(false);

  useEffect(() => {
    const promotionStrategyNode = promotionStrategyNodes[0];
    const group = "promoter.argoproj.io";
    const url = `/api/v1/applications/${application.metadata.name}/resource?name=${promotionStrategyNode.name}&appNamespace=${application.metadata.namespace}&namespace=${promotionStrategyNode.namespace}&resourceName=${promotionStrategyNode.name}&version=${promotionStrategyNode?.version}&kind=${promotionStrategyNode.kind}&group=${group}`;

    setIsLoading(true);
    fetch(url)
      .then((response) => response.json())
      .then((data) => {
        setPromotionStrategy(
          typeof data.manifest === "string"
            ? JSON.parse(data.manifest)
            : data.manifest
        );
        setIsLoading(false);
      })
      .catch((err) => {
        setIsLoading(false);
        throw new Error("Error fetching promotion strategy data: " + err);
      });
  }, [promotionStrategyNodes]);
  if (isLoading && !promotionStrategy) {
    return <div>Loading...</div>;
  }
  return (
    <div className="extension-container">
      <Card environments={promotionStrategy?.status?.environments || []} />
    </div>
  );
};

export default AppViewExtension;
