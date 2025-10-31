import type { PromotionStrategy } from "./promotion";

export interface ResourceExtensionProps {
  application: {
    metadata: {
      name: string;
      namespace: string;
    };
  };
  resource: PromotionStrategy;
}

// ArgoCD types extracted to remove dependency

export interface ApplicationResource {
  kind: string;
  group: string;
  name: string;
  namespace: string;
  status: string;
}

export interface Application {
  metadata: {
    name: string;
    namespace: string;
  };
  status?: {
    resources?: ApplicationResource[];
  };
}

export interface TreeNode {
  kind: string;
  name: string;
  namespace: string;
  group?: string;
  version?: string;
}

export interface Tree {
  nodes: TreeNode[];
}

export interface AppViewComponentProps {
  tree: Tree;
  application: Application;
}

export interface ApplicationsService {
  getResource: (
    name: string,
    namespace: string,
    node: TreeNode
  ) => Promise<PromotionStrategy>;
}

export interface Services {
  applications: ApplicationsService;
}

// Extension API types
export interface ResourceExtensionProps {
  application: {
    metadata: {
      name: string;
      namespace: string;
    };
  };
  resource: PromotionStrategy;
}

export interface StatusPanelProps {
  application: {
    metadata: {
      name: string;
      namespace: string;
    };
    status?: {
      resources?: ApplicationResource[];
    };
  };
}

export interface ExtensionsAPI {
  registerResourceExtension: (
    component: React.ComponentType<ResourceExtensionProps>,
    group: string,
    kind: string,
    title: string,
    options?: { icon: string }
  ) => void;

  registerStatusPanelExtension: (
    component: React.ComponentType<StatusPanelProps>,
    title: string,
    id: string,
    flyout?: React.ComponentType<StatusPanelProps>
  ) => void;

  registerAppViewExtension: (
    component: React.ComponentType<AppViewComponentProps>,
    title: string,
    icon: string,
    shouldDisplay?: (application: Application) => boolean
  ) => void;
}

declare global {
  interface Window {
    extensionsAPI?: ExtensionsAPI;
  }
}
