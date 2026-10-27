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
    labels?: Record<string, string>;
    annotations?: Record<string, string>;
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
  nodes?: TreeNode[];
}

export interface AppViewComponentProps {
  tree: Tree;
  application: Application;
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
  registerStatusPanelExtension: (
    component: React.ComponentType<StatusPanelProps>,
    title: string,
    id: string,
    flyout?: React.ComponentType<StatusPanelProps>,
  ) => void;

  registerAppViewExtension: (
    component: React.ComponentType<AppViewComponentProps>,
    title: string,
    icon: string,
    shouldDisplay?: (application: Application) => boolean,
  ) => void;
}

declare global {
  interface Window {
    extensionsAPI?: ExtensionsAPI;
  }
}
