// Register tsx for TypeScript support
require('tsx/cjs');

// Hook into require to handle non-JS files and ensure single React instance
const Module = require('module');
const path = require('path');
const originalRequire = Module.prototype.require;

const styleExtensions = ['.scss', '.css', '.sass', '.less'];
const assetExtensions = ['.png', '.jpg', '.jpeg', '.gif', '.svg', '.webp', '.ico'];

// Resolve React and ReactDOM from the extension's node_modules to ensure a single instance
const extensionDir = path.resolve(__dirname, '..');
const reactPath = require.resolve('react', { paths: [extensionDir] });
const reactDomPath = require.resolve('react-dom/client', { paths: [extensionDir] });
const reactDomServerPath = require.resolve('react-dom', { paths: [extensionDir] });

Module.prototype.require = function(id) {
    const ext = id.match(/\.[^./\\]+$/)?.[0];

    // Return empty object for style files
    if (ext && styleExtensions.includes(ext)) {
        return {};
    }

    // Return mock path string for asset files
    if (ext && assetExtensions.includes(ext)) {
        return id;
    }

    // Ensure single React instance across all packages
    if (id === 'react') {
        return originalRequire.call(this, reactPath);
    }
    if (id === 'react-dom/client') {
        return originalRequire.call(this, reactDomPath);
    }
    if (id === 'react-dom') {
        return originalRequire.call(this, reactDomServerPath);
    }

    return originalRequire.apply(this, arguments);
};

// Set up jsdom environment
const { JSDOM } = require('jsdom');

const dom = new JSDOM('<!DOCTYPE html><html><body><div id="root"></div></body></html>', {
    url: 'http://localhost:3000',
    pretendToBeVisual: true,
});

// Set up global DOM environment
global.window = dom.window;
global.document = dom.window.document;
global.navigator = dom.window.navigator;
global.HTMLElement = dom.window.HTMLElement;
global.Element = dom.window.Element;
global.Node = dom.window.Node;
global.Text = dom.window.Text;
global.DocumentFragment = dom.window.DocumentFragment;

// Mock extensionsAPI
global.window.extensionsAPI = {
    registerResourceExtension: () => {},
    registerStatusPanelExtension: () => {},
    registerAppViewExtension: () => {},
};

// Mock fetch API
global.fetch = async () => {
    return {
        ok: true,
        json: async () => ({}),
        text: async () => '',
    };
};

// Mock matchMedia
global.window.matchMedia = (query) => ({
    matches: false,
    media: query,
    onchange: null,
    addListener: () => {},
    removeListener: () => {},
    addEventListener: () => {},
    removeEventListener: () => {},
    dispatchEvent: () => true,
});

// Mock ResizeObserver
global.ResizeObserver = class ResizeObserver {
    observe() {}
    unobserve() {}
    disconnect() {}
};

// Mock IntersectionObserver
global.IntersectionObserver = class IntersectionObserver {
    constructor(callback, options) {}
    observe() {}
    unobserve() {}
    disconnect() {}
};
