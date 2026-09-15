/**
 * @vitest-environment jsdom
 */
import { describe, it, beforeEach, afterEach, expect, vi } from 'vitest';
import React, { act } from 'react';
import { createRoot } from 'react-dom/client';
import { PluginErrorBoundary } from './PluginErrorBoundary';

const Boom: React.FC = () => {
  throw new Error('plugin exploded');
};

describe('PluginErrorBoundary', () => {
  let container: HTMLDivElement;
  let root: ReturnType<typeof createRoot>;

  beforeEach(() => {
    container = document.createElement('div');
    document.body.appendChild(container);
  });

  afterEach(() => {
    root?.unmount();
    container.remove();
    vi.restoreAllMocks();
  });

  it('renders children normally when no error occurs', () => {
    root = createRoot(container);
    act(() => {
      root.render(
        React.createElement(PluginErrorBoundary, null, React.createElement('span', null, 'ok')),
      );
    });

    expect(container.textContent).toBe('ok');
  });

  it('renders the fallback instead of children when a child throws during render', () => {
    const consoleErrorSpy = vi.spyOn(console, 'error').mockImplementation(() => {});

    root = createRoot(container);
    act(() => {
      root.render(React.createElement(PluginErrorBoundary, null, React.createElement(Boom)));
    });

    expect(container.textContent).toBe('');
    consoleErrorSpy.mockRestore();
  });

  it('logs the caught error with the plugin kind when provided', () => {
    const consoleErrorSpy = vi.spyOn(console, 'error').mockImplementation(() => {});

    root = createRoot(container);
    act(() => {
      root.render(
        React.createElement(PluginErrorBoundary, {
          pluginKind: 'TimedCommitStatus',
          children: React.createElement(Boom),
        }),
      );
    });

    expect(consoleErrorSpy).toHaveBeenCalledWith(
      expect.stringContaining('TimedCommitStatus'),
      expect.any(Error),
    );
  });
});
