import fs from 'fs';
import path from 'path';

// Appends every plugin bundle from the shared plugins directory onto the
// webpack output, wrapped in its own try/catch so one broken plugin doesn't
// break the extension's own registration code that already ran above it.
// This makes extension-promoter.js fully self-contained: nothing here reaches
// back to the promoter's webserver at runtime.

export function buildAppendedPlugins(pluginsDir) {
  const entries = fs.existsSync(pluginsDir)
    ? fs.readdirSync(pluginsDir, { withFileTypes: true })
    : [];

  let appended = '';
  for (const entry of entries) {
    if (!entry.isFile() || !entry.name.startsWith('plugin') || !entry.name.endsWith('.js')) {
      continue;
    }
    const content = fs.readFileSync(path.join(pluginsDir, entry.name), 'utf8');

    // new Function only compiles the source to check its syntax; it never executes
    // it. A malformed bundle (truncated download, ES-module `import`, stray token)
    // is a SyntaxError here, not a runtime throw, so the try/catch below it can't
    // catch it once concatenated - it would take down the whole extension bundle.
    try {
      new Function(content);
    } catch (e) {
      console.warn(`Skipping plugin ${entry.name}: failed syntax validation (${e.message})`);
      continue;
    }

    appended += `\n// source: ${entry.name}\ntry {\n${content}\n} catch(e) { console.error('Plugin ${entry.name} failed to load:', e); }\n`;
  }

  return appended;
}

if (
  process.argv[1] &&
  path.resolve(process.argv[1]) === path.resolve(new URL(import.meta.url).pathname)
) {
  const pluginsDir = path.resolve(process.cwd(), '../plugins');
  const outputFile = path.resolve(process.cwd(), 'dist/extension-promoter.js');

  const appended = buildAppendedPlugins(pluginsDir);
  if (appended) {
    fs.appendFileSync(outputFile, appended);
  }
}
