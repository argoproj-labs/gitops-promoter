import React from 'react';

interface PluginErrorBoundaryProps {
  pluginKind?: string;
  fallback?: React.ReactNode;
  children: React.ReactNode;
}

interface PluginErrorBoundaryState {
  hasError: boolean;
}

// getDerivedStateFromError/componentDidCatch have no hooks equivalent, so this
// must be a class component. Callers should key this component on plugin
// identity (e.g. `check.kind`) so a plugin swap remounts the boundary and
// clears any latched error state, rather than staying stuck on the fallback.
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
      return this.props.fallback ?? null;
    }
    return this.props.children;
  }
}
