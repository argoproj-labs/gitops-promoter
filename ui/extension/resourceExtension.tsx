import React from "react";
import Card from "@components-lib/components/Card";
import { ResourceExtensionProps } from "@shared/types/extension";

// Argo CD extension component

const ResourceExtension: React.FC<ResourceExtensionProps> = ({ resource }) => {
  //Pass raw data to Card component
  const environments = resource.status?.environments || [];

  return (
    <div className="extension-container">
      <Card environments={environments} />
    </div>
  );
};

export default ResourceExtension;
