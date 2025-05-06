export interface PromotionStrategyType {
    metadata: {
        name: string;
        namespace?: string;
        labels?: Record<string, string>;
        annotations?: Record<string, string>;
    };
    spec: {
        gitRepositoryRef: {
            name: string;
            namespace?: string;
        };
        activeCommitStatuses?: { key: string }[];
        proposedCommitStatuses?: { key: string }[];
        environments: {
            branch: string;
            autoMerge?: boolean;
            activeCommitStatuses?: { key: string }[];
            proposedCommitStatuses?: { key: string }[];
        }[];
    };
    status?: {
        environments: {
            branch: string;
            active: {
                dry: { sha: string };
                hydrated: { sha: string };
                commitStatus: { sha: string; phase: "pending" | "success" | "failure" };
            };
            proposed: {
                dry: { sha: string };
                hydrated: { sha: string };
                commitStatus: { sha: string; phase: "pending" | "success" | "failure" };
            };
            lastHealthyDryShas?: { sha: string; time: string }[];
        }[];
    };
}