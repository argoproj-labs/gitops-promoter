import fs from 'fs';
import path from 'path';

// Copies the Vite build output into ../web/static (the Go embed source), plus any
// external plugin bundles from the shared ../plugins directory. Removes the
// destination first so a plugin dropped from ../plugins between builds doesn't
// linger in ../web/static and keep being served by buildPluginsBundle.

const distDir = path.resolve(process.cwd(), 'dist');
const staticDir = path.resolve(process.cwd(), '../web/static');
const pluginsDir = path.resolve(process.cwd(), '../plugins');

function copyRecursive(src, dest) {
  fs.mkdirSync(dest, { recursive: true });
  for (const entry of fs.readdirSync(src, { withFileTypes: true })) {
    const srcPath = path.join(src, entry.name);
    const destPath = path.join(dest, entry.name);
    if (entry.isDirectory()) {
      copyRecursive(srcPath, destPath);
    } else {
      fs.copyFileSync(srcPath, destPath);
    }
  }
}

fs.rmSync(staticDir, { recursive: true, force: true });
copyRecursive(distDir, staticDir);

if (fs.existsSync(pluginsDir)) {
  for (const entry of fs.readdirSync(pluginsDir, { withFileTypes: true })) {
    if (!entry.isFile() || !entry.name.startsWith('plugin') || !entry.name.endsWith('.js')) {
      continue;
    }
    fs.copyFileSync(path.join(pluginsDir, entry.name), path.join(staticDir, entry.name));
  }
}
