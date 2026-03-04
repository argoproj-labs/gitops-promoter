import { expect } from 'chai';
import React from 'react';
import { createRoot } from 'react-dom/client';

describe('Extension Page Load Tests', () => {
    let container: HTMLDivElement;

    beforeEach(() => {
        container = document.createElement('div');
        container.id = 'test-root';
        document.body.appendChild(container);
    });

    afterEach(() => {
        if (container && container.parentNode) {
            container.parentNode.removeChild(container);
        }
    });

    describe('AppViewExtension Component', () => {
        it('should render the AppViewExtension component without crashing', () => {
            // eslint-disable-next-line @typescript-eslint/no-var-requires
            const AppViewExtension = require('../AppViewExtension').default;

            const mockTree = {
                nodes: [],
            };

            const mockApplication = {
                metadata: {
                    name: 'test-app',
                    namespace: 'argocd',
                },
                status: {
                    resources: [],
                },
            };

            const root = createRoot(container);

            // Should not throw an error
            expect(() => {
                root.render(React.createElement(AppViewExtension, {
                    tree: mockTree,
                    application: mockApplication,
                }));
            }).to.not.throw();

            // Clean up
            root.unmount();
        });

        it('should display message when no PromotionStrategy found', async () => {
            // eslint-disable-next-line @typescript-eslint/no-var-requires
            const AppViewExtension = require('../AppViewExtension').default;

            const mockTree = {
                nodes: [],
            };

            const mockApplication = {
                metadata: {
                    name: 'test-app',
                    namespace: 'argocd',
                },
                status: {
                    resources: [],
                },
            };

            const root = createRoot(container);
            root.render(React.createElement(AppViewExtension, {
                tree: mockTree,
                application: mockApplication,
            }));

            // Wait for render
            await new Promise(resolve => setTimeout(resolve, 100));

            // Check that it shows no PromotionStrategy message
            expect(container.innerHTML).to.include('No PromotionStrategy');

            root.unmount();
        });

        it('should display error message when multiple PromotionStrategies found', async () => {
            // eslint-disable-next-line @typescript-eslint/no-var-requires
            const AppViewExtension = require('../AppViewExtension').default;

            const mockTree = {
                nodes: [
                    { kind: 'PromotionStrategy', name: 'ps1', namespace: 'default', version: 'v1alpha1' },
                    { kind: 'PromotionStrategy', name: 'ps2', namespace: 'default', version: 'v1alpha1' },
                ],
            };

            const mockApplication = {
                metadata: {
                    name: 'test-app',
                    namespace: 'argocd',
                },
                status: {
                    resources: [],
                },
            };

            const root = createRoot(container);
            root.render(React.createElement(AppViewExtension, {
                tree: mockTree,
                application: mockApplication,
            }));

            // Wait for render
            await new Promise(resolve => setTimeout(resolve, 100));

            // Check that it shows expected exactly one message
            expect(container.innerHTML).to.include('Expected exactly one PromotionStrategy');

            root.unmount();
        });
    });

    describe('ResourceExtension Component', () => {
        it('should render the ResourceExtension component without crashing', () => {
            // eslint-disable-next-line @typescript-eslint/no-var-requires
            const ResourceExtension = require('../resourceExtension').default;

            const mockResource = {
                status: {
                    environments: [],
                },
            };

            const root = createRoot(container);

            // Should not throw an error
            expect(() => {
                root.render(React.createElement(ResourceExtension, {
                    resource: mockResource,
                }));
            }).to.not.throw();

            // Clean up
            root.unmount();
        });
    });

    describe('Index Module', () => {
        it('should load the index module without errors', () => {
            expect(() => {
                require('../index');
            }).to.not.throw();
        });
    });
});
