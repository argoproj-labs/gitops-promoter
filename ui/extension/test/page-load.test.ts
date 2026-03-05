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
        it('should render without crashing when no PromotionStrategy nodes exist', () => {
            const AppViewExtension = require('../AppViewExtension').default;

            const props = {
                tree: { nodes: [] },
                application: {
                    metadata: { name: 'test-app', namespace: 'default' },
                },
            };

            const root = createRoot(container);

            expect(() => {
                root.render(React.createElement(AppViewExtension, props));
            }).to.not.throw();

            root.unmount();
        });

        it('should render without crashing when one PromotionStrategy node exists', async () => {
            const AppViewExtension = require('../AppViewExtension').default;

            const props = {
                tree: {
                    nodes: [
                        {
                            kind: 'PromotionStrategy',
                            name: 'my-strategy',
                            namespace: 'default',
                            group: 'promoter.argoproj.io',
                            version: 'v1alpha1',
                        },
                    ],
                },
                application: {
                    metadata: { name: 'test-app', namespace: 'default' },
                },
            };

            const root = createRoot(container);
            root.render(React.createElement(AppViewExtension, props));

            // Wait for render
            await new Promise(resolve => setTimeout(resolve, 100));

            expect(container.innerHTML).to.not.equal('');

            root.unmount();
        });

        it('should render without crashing when multiple PromotionStrategy nodes exist', () => {
            const AppViewExtension = require('../AppViewExtension').default;

            const props = {
                tree: {
                    nodes: [
                        {
                            kind: 'PromotionStrategy',
                            name: 'strategy-1',
                            namespace: 'default',
                            group: 'promoter.argoproj.io',
                            version: 'v1alpha1',
                        },
                        {
                            kind: 'PromotionStrategy',
                            name: 'strategy-2',
                            namespace: 'default',
                            group: 'promoter.argoproj.io',
                            version: 'v1alpha1',
                        },
                    ],
                },
                application: {
                    metadata: { name: 'test-app', namespace: 'default' },
                },
            };

            const root = createRoot(container);

            expect(() => {
                root.render(React.createElement(AppViewExtension, props));
            }).to.not.throw();

            root.unmount();
        });
    });

    describe('ResourceExtension Component', () => {
        it('should render without crashing with an empty resource', () => {
            const ResourceExtension = require('../resourceExtension').default;

            const props = {
                application: {
                    metadata: { name: 'test-app', namespace: 'default' },
                },
                resource: {
                    kind: 'PromotionStrategy',
                    apiVersion: 'promoter.argoproj.io/v1alpha1',
                    metadata: {
                        name: 'my-strategy',
                        namespace: 'default',
                        uid: 'test-uid',
                        resourceVersion: '1',
                        generation: 1,
                        creationTimestamp: '2024-01-01T00:00:00Z',
                    },
                    spec: {
                        gitRepositoryRef: { name: 'my-repo' },
                        environments: [],
                    },
                },
            };

            const root = createRoot(container);

            expect(() => {
                root.render(React.createElement(ResourceExtension, props));
            }).to.not.throw();

            root.unmount();
        });

        it('should render without crashing with status environments', async () => {
            const ResourceExtension = require('../resourceExtension').default;

            const props = {
                application: {
                    metadata: { name: 'test-app', namespace: 'default' },
                },
                resource: {
                    kind: 'PromotionStrategy',
                    apiVersion: 'promoter.argoproj.io/v1alpha1',
                    metadata: {
                        name: 'my-strategy',
                        namespace: 'default',
                        uid: 'test-uid',
                        resourceVersion: '1',
                        generation: 1,
                        creationTimestamp: '2024-01-01T00:00:00Z',
                    },
                    spec: {
                        gitRepositoryRef: { name: 'my-repo' },
                        environments: [{ branch: 'main' }],
                    },
                    status: {
                        environments: [
                            {
                                branch: 'main',
                                active: {},
                                proposed: {},
                            },
                        ],
                    },
                },
            };

            const root = createRoot(container);
            root.render(React.createElement(ResourceExtension, props));

            // Wait for render
            await new Promise(resolve => setTimeout(resolve, 100));

            expect(container.innerHTML).to.not.equal('');

            root.unmount();
        });
    });
});
