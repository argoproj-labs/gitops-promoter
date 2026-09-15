import React from 'react';

interface PluginErrorBoundaryProps {
  pluginKind?: string;
  children: React.ReactNode;
}

interface PluginErrorBoundaryState {
  hasError: boolean;
}

// getDerivedStateFromError/componentDidCatch have no hooks equivalent, so this
// must be a class component.
export class PluginErrorBoundary extends React.Component<
  PluginErrorBoundaryProps,
  PluginErrorBoundaryState
> {
  constructor(props: PluginErrorBoundaryProps) {
    super(props);
    this.state = { hasError: false };
  }

  static getDerivedStateFromError(): PluginErrorBoundaryState {
    return { hasError: true };
  }

  componentDidCatch(error: unknown): void {
    console.error(`Plugin "${this.props.pluginKind ?? 'unknown'}" failed to render:`, error);
  }

  render(): React.ReactNode {
    if (this.state.hasError) {
      return null;
    }
    return this.props.children;
  }
}
