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

  it('renders null when a child throws and no fallback is provided', () => {
    const consoleErrorSpy = vi.spyOn(console, 'error').mockImplementation(() => {});

    root = createRoot(container);
    act(() => {
      root.render(React.createElement(PluginErrorBoundary, null, React.createElement(Boom)));
    });

    expect(container.textContent).toBe('');
    consoleErrorSpy.mockRestore();
  });

  it('renders the fallback instead of null when a child throws during render', () => {
    const consoleErrorSpy = vi.spyOn(console, 'error').mockImplementation(() => {});

    root = createRoot(container);
    act(() => {
      root.render(
        React.createElement(PluginErrorBoundary, {
          fallback: React.createElement('span', null, 'default row'),
          children: React.createElement(Boom),
        }),
      );
    });

    expect(container.textContent).toBe('default row');
    consoleErrorSpy.mockRestore();
  });

  it('clears the error state and renders normally when remounted via a key change', () => {
    const consoleErrorSpy = vi.spyOn(console, 'error').mockImplementation(() => {});

    root = createRoot(container);
    act(() => {
      root.render(
        React.createElement(PluginErrorBoundary, {
          key: 'a',
          fallback: React.createElement('span', null, 'default row'),
          children: React.createElement(Boom),
        }),
      );
    });
    expect(container.textContent).toBe('default row');

    // Simulating a plugin swap: a different `key` remounts the boundary,
    // clearing `hasError` so a subsequently working plugin isn't stuck
    // behind a stale error latched by the previous plugin.
    act(() => {
      root.render(
        React.createElement(PluginErrorBoundary, {
          key: 'b',
          fallback: React.createElement('span', null, 'default row'),
          children: React.createElement('span', null, 'ok'),
        }),
      );
    });

    expect(container.textContent).toBe('ok');
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
