import { describe, it, expect, beforeEach, vi } from 'vitest';
import React from 'react';
import {
  ANY_VERSION,
  PROMOTER_GROUP,
  getCommitStatusRowPlugin,
  listCommitStatusRowPlugins,
  listCommitStatusRowPluginsByAnnotation,
  registerCommitStatusRowPlugin,
  registerCommitStatusRowPluginByAnnotation,
  resetPluginRegistry,
} from './registry';
import type { RowPlugin } from './types';

const headerA = () => null;
const headerB = () => null;
const pluginA: RowPlugin = { rowHeader: headerA };
const pluginB: RowPlugin = { rowHeader: headerB };

describe('commit status row plugin registry', () => {
  beforeEach(() => {
    resetPluginRegistry();
  });

  it('returns a plugin registered for a kind in the promoter group', () => {
    registerCommitStatusRowPlugin(pluginA, 'TimedCommitStatus');

    expect(getCommitStatusRowPlugin('TimedCommitStatus')).toBe(pluginA);
    expect(getCommitStatusRowPlugin('TimedCommitStatus', `${PROMOTER_GROUP}/v1alpha1`)).toBe(
      pluginA,
    );
  });

  it('returns undefined for an unregistered kind or a missing kind', () => {
    registerCommitStatusRowPlugin(pluginA, 'TimedCommitStatus');

    expect(getCommitStatusRowPlugin('GitCommitStatus')).toBeUndefined();
    expect(getCommitStatusRowPlugin(undefined)).toBeUndefined();
  });

  it('does not match a kind registered under a different group', () => {
    registerCommitStatusRowPlugin(pluginA, 'TimedCommitStatus', 'example.com');

    expect(getCommitStatusRowPlugin('TimedCommitStatus', 'example.com/v1')).toBe(pluginA);
    expect(
      getCommitStatusRowPlugin('TimedCommitStatus', `${PROMOTER_GROUP}/v1alpha1`),
    ).toBeUndefined();
  });

  it('lets a later registration override an earlier one for the same kind', () => {
    registerCommitStatusRowPlugin(pluginA, 'TimedCommitStatus');
    registerCommitStatusRowPlugin(pluginB, 'TimedCommitStatus');

    expect(getCommitStatusRowPlugin('TimedCommitStatus')).toBe(pluginB);
    expect(listCommitStatusRowPlugins()).toHaveLength(1);
  });

  it('matches any version by default so a CRD version bump keeps rendering', () => {
    registerCommitStatusRowPlugin(pluginA, 'TimedCommitStatus');

    expect(getCommitStatusRowPlugin('TimedCommitStatus', `${PROMOTER_GROUP}/v1beta1`)).toBe(
      pluginA,
    );
  });

  it('prefers a version-pinned registration over a version-agnostic one', () => {
    registerCommitStatusRowPlugin(pluginA, 'TimedCommitStatus');
    registerCommitStatusRowPlugin(pluginB, 'TimedCommitStatus', PROMOTER_GROUP, 'v1alpha1');

    expect(getCommitStatusRowPlugin('TimedCommitStatus', `${PROMOTER_GROUP}/v1alpha1`)).toBe(
      pluginB,
    );
    expect(getCommitStatusRowPlugin('TimedCommitStatus', `${PROMOTER_GROUP}/v1beta1`)).toBe(
      pluginA,
    );
  });

  it('falls back to the promoter group when apiVersion carries no version', () => {
    registerCommitStatusRowPlugin(pluginA, 'TimedCommitStatus');

    expect(getCommitStatusRowPlugin('TimedCommitStatus', PROMOTER_GROUP)).toBe(pluginA);
    expect(getCommitStatusRowPlugin('TimedCommitStatus', '')).toBe(pluginA);
  });

  it('records the registration GVK for diagnostics', () => {
    registerCommitStatusRowPlugin(pluginA, 'TimedCommitStatus');

    expect(listCommitStatusRowPlugins()).toEqual([
      {
        group: PROMOTER_GROUP,
        version: ANY_VERSION,
        kind: 'TimedCommitStatus',
        plugin: pluginA,
      },
    ]);
  });

  describe('GVK+annotation-keyed registration', () => {
    it('returns a plugin registered for a matching kind and annotation', () => {
      registerCommitStatusRowPluginByAnnotation(
        pluginA,
        'WebRequestCommitStatus',
        'example.com/plugin',
        'my-plugin',
      );

      expect(
        getCommitStatusRowPlugin('WebRequestCommitStatus', undefined, {
          'example.com/plugin': 'my-plugin',
        }),
      ).toBe(pluginA);
    });

    it('does not match a different kind even if the annotation matches', () => {
      registerCommitStatusRowPluginByAnnotation(
        pluginA,
        'WebRequestCommitStatus',
        'example.com/plugin',
        'my-plugin',
      );

      expect(
        getCommitStatusRowPlugin('GitCommitStatus', undefined, {
          'example.com/plugin': 'my-plugin',
        }),
      ).toBeUndefined();
    });

    it('returns undefined when the kind matches but no annotation does', () => {
      registerCommitStatusRowPluginByAnnotation(
        pluginA,
        'WebRequestCommitStatus',
        'example.com/plugin',
        'my-plugin',
      );

      expect(
        getCommitStatusRowPlugin('WebRequestCommitStatus', undefined, {
          'example.com/plugin': 'other-plugin',
        }),
      ).toBeUndefined();
    });

    it('prefers a GVK+annotation match over a plain GVK match for the same kind', () => {
      registerCommitStatusRowPlugin(pluginA, 'WebRequestCommitStatus');
      registerCommitStatusRowPluginByAnnotation(
        pluginB,
        'WebRequestCommitStatus',
        'example.com/plugin',
        'my-plugin',
      );

      expect(
        getCommitStatusRowPlugin('WebRequestCommitStatus', undefined, {
          'example.com/plugin': 'my-plugin',
        }),
      ).toBe(pluginB);
    });

    it('falls back to a plain GVK match when no annotation on the resource matches', () => {
      registerCommitStatusRowPlugin(pluginA, 'WebRequestCommitStatus');
      registerCommitStatusRowPluginByAnnotation(
        pluginB,
        'WebRequestCommitStatus',
        'example.com/plugin',
        'my-plugin',
      );

      expect(
        getCommitStatusRowPlugin('WebRequestCommitStatus', undefined, {
          'some-other/annotation': 'value',
        }),
      ).toBe(pluginA);
    });

    it('lets a later registration override an earlier one for the same GVK/annotation combination', () => {
      registerCommitStatusRowPluginByAnnotation(
        pluginA,
        'WebRequestCommitStatus',
        'example.com/plugin',
        'my-plugin',
      );
      registerCommitStatusRowPluginByAnnotation(
        pluginB,
        'WebRequestCommitStatus',
        'example.com/plugin',
        'my-plugin',
      );

      expect(
        getCommitStatusRowPlugin('WebRequestCommitStatus', undefined, {
          'example.com/plugin': 'my-plugin',
        }),
      ).toBe(pluginB);
      expect(listCommitStatusRowPluginsByAnnotation()).toHaveLength(1);
    });

    it('prefers a version-pinned annotation registration over a version-agnostic one', () => {
      registerCommitStatusRowPluginByAnnotation(
        pluginA,
        'WebRequestCommitStatus',
        'example.com/plugin',
        'my-plugin',
      );
      registerCommitStatusRowPluginByAnnotation(
        pluginB,
        'WebRequestCommitStatus',
        'example.com/plugin',
        'my-plugin',
        PROMOTER_GROUP,
        'v1alpha1',
      );

      expect(
        getCommitStatusRowPlugin('WebRequestCommitStatus', `${PROMOTER_GROUP}/v1alpha1`, {
          'example.com/plugin': 'my-plugin',
        }),
      ).toBe(pluginB);
      expect(
        getCommitStatusRowPlugin('WebRequestCommitStatus', `${PROMOTER_GROUP}/v1beta1`, {
          'example.com/plugin': 'my-plugin',
        }),
      ).toBe(pluginA);
    });

    it('records the GVK+annotation registration for diagnostics', () => {
      registerCommitStatusRowPluginByAnnotation(
        pluginA,
        'WebRequestCommitStatus',
        'example.com/plugin',
        'my-plugin',
      );

      expect(listCommitStatusRowPluginsByAnnotation()).toEqual([
        {
          group: PROMOTER_GROUP,
          version: ANY_VERSION,
          kind: 'WebRequestCommitStatus',
          annotationKey: 'example.com/plugin',
          annotationValue: 'my-plugin',
          plugin: pluginA,
        },
      ]);
    });
  });

  describe('rejects malformed registrations', () => {
    it('ignores a plugin whose rowHeader is not a function', () => {
      const consoleErrorSpy = vi.spyOn(console, 'error').mockImplementation(() => {});

      registerCommitStatusRowPlugin(
        { rowHeader: 'not a component' } as unknown as RowPlugin,
        'TimedCommitStatus',
      );

      expect(getCommitStatusRowPlugin('TimedCommitStatus')).toBeUndefined();
      expect(consoleErrorSpy).toHaveBeenCalled();
      consoleErrorSpy.mockRestore();
    });

    it('ignores a plugin whose rowContent is not a function', () => {
      const consoleErrorSpy = vi.spyOn(console, 'error').mockImplementation(() => {});

      registerCommitStatusRowPlugin(
        { rowHeader: headerA, rowContent: 'not a component' } as unknown as RowPlugin,
        'TimedCommitStatus',
      );

      expect(getCommitStatusRowPlugin('TimedCommitStatus')).toBeUndefined();
      expect(consoleErrorSpy).toHaveBeenCalled();
      consoleErrorSpy.mockRestore();
    });

    it('ignores a malformed annotation-keyed plugin without registering it', () => {
      const consoleErrorSpy = vi.spyOn(console, 'error').mockImplementation(() => {});

      registerCommitStatusRowPluginByAnnotation(
        { rowHeader: undefined } as unknown as RowPlugin,
        'WebRequestCommitStatus',
        'example.com/plugin',
        'my-plugin',
      );

      expect(listCommitStatusRowPluginsByAnnotation()).toEqual([]);
      expect(consoleErrorSpy).toHaveBeenCalled();
      consoleErrorSpy.mockRestore();
    });

    it('leaves a prior valid registration for the same GVK in place', () => {
      registerCommitStatusRowPlugin(pluginA, 'TimedCommitStatus');
      const consoleErrorSpy = vi.spyOn(console, 'error').mockImplementation(() => {});

      registerCommitStatusRowPlugin(
        { rowHeader: null } as unknown as RowPlugin,
        'TimedCommitStatus',
      );

      expect(getCommitStatusRowPlugin('TimedCommitStatus')).toBe(pluginA);
      consoleErrorSpy.mockRestore();
    });

    it('ignores an undefined plugin without throwing', () => {
      const consoleErrorSpy = vi.spyOn(console, 'error').mockImplementation(() => {});

      expect(() => registerCommitStatusRowPlugin(undefined, 'TimedCommitStatus')).not.toThrow();

      expect(getCommitStatusRowPlugin('TimedCommitStatus')).toBeUndefined();
      expect(consoleErrorSpy).toHaveBeenCalled();
      consoleErrorSpy.mockRestore();
    });

    it('ignores a null plugin without throwing', () => {
      const consoleErrorSpy = vi.spyOn(console, 'error').mockImplementation(() => {});

      expect(() => registerCommitStatusRowPlugin(null, 'TimedCommitStatus')).not.toThrow();

      expect(getCommitStatusRowPlugin('TimedCommitStatus')).toBeUndefined();
      expect(consoleErrorSpy).toHaveBeenCalled();
      consoleErrorSpy.mockRestore();
    });
  });

  describe('accepts memo/forwardRef components', () => {
    it('registers a plugin whose rowHeader is a React.memo component', () => {
      const memoHeader = React.memo(headerA);

      registerCommitStatusRowPlugin({ rowHeader: memoHeader }, 'TimedCommitStatus');

      expect(getCommitStatusRowPlugin('TimedCommitStatus')?.rowHeader).toBe(memoHeader);
    });

    it('registers a plugin whose rowContent is a React.forwardRef component', () => {
      const fwdContent = React.forwardRef(() => null);

      registerCommitStatusRowPlugin(
        { rowHeader: headerA, rowContent: fwdContent as unknown as RowPlugin['rowContent'] },
        'TimedCommitStatus',
      );

      expect(getCommitStatusRowPlugin('TimedCommitStatus')?.rowContent).toBe(fwdContent);
    });
  });
});
