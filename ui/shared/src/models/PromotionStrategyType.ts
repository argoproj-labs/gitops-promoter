export interface PromotionStrategyType {
    kind: string;
    apiVersion: string;
    metadata: {
        name: string;
        namespace: string;
        uid: string;
        resourceVersion: string;
        generation: number;
        creationTimestamp: string;
        labels?: Record<string, string>;
        annotations?: Record<string, string>;
    };
    spec: {
        gitRepositoryRef: {
            name: string;
            namespace?: string;
        };
        activeCommitStatuses?: { key: string }[] | null;
        proposedCommitStatuses?: { key: string }[] | null;
        environments: {
            branch: string;
            autoMerge?: boolean;
            activeCommitStatuses?: { key: string }[] | null;
            proposedCommitStatuses?: { key: string }[] | null;
        }[];
    };
    status: {
        environments: {
            branch: string;
            proposed: {
                dry?: {
                    sha?: string;
                    commitTime?: string | null;
                    repoURL?: string;
                    author?: string;
                    subject?: string;
                    body?: string;
                    references?: {
                        commit: {
                            author?: string;
                            sha?: string;
                            date?: string;
                            body?: string;
                            repoURL?: string;
                        }
                    }[];
                };
                hydrated?: {
                    sha?: string;
                    commitTime?: string | null;
                    author?: string;
                    subject?: string;
                    body?: string;
                };
                commitStatuses?: { key: string; phase: string; url?: string; details?: string }[];
            };
            active: {
                dry?: {
                    sha?: string;
                    commitTime?: string | null;
                    repoURL?: string;
                    author?: string;
                    subject?: string;
                    body?: string;
                    references?: {
                        commit: {
                            author?: string;
                            sha?: string;
                            date?: string;
                            body?: string;
                            repoURL?: string;
                        }
                    }[];
                };
                hydrated?: {
                    sha?: string;
                    commitTime?: string | null;
                    author?: string;
                    subject?: string;
                    body?: string;
                };
                commitStatuses?: { key: string; phase: string; url?: string; details?: string }[];
            };
            pullRequest?: {
                id: string;
                state: string;
                prCreationTime: string;
                url?: string;
            };
            lastHealthyDryShas?: any[] | null;
        }[];
        conditions: {
            type: string;
            status: string;
            observedGeneration: number;
            lastTransitionTime: string;
            reason: string;
            message: string;
        }[];
    };
} 