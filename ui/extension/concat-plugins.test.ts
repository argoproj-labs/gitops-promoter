import fs from 'fs';
import os from 'os';
import path from 'path';
import { describe, it, expect, afterEach } from 'vitest';

import { buildAppendedPlugins } from './concat-plugins.mjs';

describe('buildAppendedPlugins', () => {
  const dirs: string[] = [];

  afterEach(() => {
    while (dirs.length) {
      const dir = dirs.pop();
      if (dir) fs.rmSync(dir, { recursive: true, force: true });
    }
  });

  function makeTempPluginsDir(): string {
    const dir = fs.mkdtempSync(path.join(os.tmpdir(), 'concat-plugins-test-'));
    dirs.push(dir);
    return dir;
  }

  it('includes a syntactically valid plugin and skips one with a syntax error, without throwing', () => {
    const dir = makeTempPluginsDir();
    fs.writeFileSync(path.join(dir, 'plugin-valid.js'), "console.log('valid plugin loaded');");
    fs.writeFileSync(path.join(dir, 'plugin-broken.js'), "import { x } from 'y'; console.log(x;");

    const appended = buildAppendedPlugins(dir);

    expect(appended).toContain('valid plugin loaded');
    expect(appended).not.toContain('plugin-broken.js');
    expect(appended).not.toContain('console.log(x;');
  });

  it('returns an empty string when the plugins directory does not exist', () => {
    const appended = buildAppendedPlugins(path.join(os.tmpdir(), 'does-not-exist-concat-plugins'));
    expect(appended).toBe('');
  });
});
