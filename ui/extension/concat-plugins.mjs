import fs from 'fs';
import path from 'path';

// Appends every plugin bundle from the shared plugins directory onto the
// webpack output, wrapped in its own try/catch so one broken plugin doesn't
// break the extension's own registration code that already ran above it.
// This makes extension-promoter.js fully self-contained: nothing here reaches
// back to the promoter's webserver at runtime.

const pluginsDir = path.resolve(process.cwd(), '../plugins');
const outputFile = path.resolve(process.cwd(), 'dist/extension-promoter.js');

const entries = fs.existsSync(pluginsDir)
  ? fs.readdirSync(pluginsDir, { withFileTypes: true })
  : [];

let appended = '';
for (const entry of entries) {
  if (!entry.isFile() || !entry.name.startsWith('plugin') || !entry.name.endsWith('.js')) {
    continue;
  }
  const content = fs.readFileSync(path.join(pluginsDir, entry.name), 'utf8');
  appended += `\n// source: ${entry.name}\ntry {\n${content}\n} catch(e) { console.error('Plugin ${entry.name} failed to load:', e); }\n`;
}

if (appended) {
  fs.appendFileSync(outputFile, appended);
}
