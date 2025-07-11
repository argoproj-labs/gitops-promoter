interface CommitStatus {
    key: string;
    phase: string;
    url?: string;
    details?: string;
}
interface Commit {
    sha?: string;
    author?: string;
    subject?: string;
    body?: string;
    commitTime?: string;
    repoURL?: string;
}
interface Environment {
    branch: string;
    active: {
        dry?: Commit;
        hydrated?: Commit;
        commitStatuses?: CommitStatus[];
    };
    proposed: {
        dry?: Commit;
        hydrated?: Commit;
        commitStatuses?: CommitStatus[];
    };
}
interface PromotionStrategy {
    metadata?: {
        namespace?: string;
    };
    spec?: {
        environments?: {
            branch: string;
            autoMerge?: boolean;
        }[];
    };
    status?: {
        environments?: Environment[];
    };
}
interface Check {
    name: string;
    status: string;
    details?: string;
    detailLinks?: string;
}
interface EnrichedEnvDetails {
    branch: string;
    phase: string;
    lastSync: string;
    drySha: string;
    dryCommitAuthor: string;
    dryCommitMessage: string;
    dryCommitSubject: string;
    dryCommitUrl: string;
    dryCommitDate: string;
    dryCommitTrailers: any;
    dryCommitTrailerBody: string | undefined;
    proposedSha: string;
    hydratedSha: string;
    hydratedCommitAuthor: string;
    hydratedCommitSubject: string;
    hydratedCommitBody: string;
    hydratedCommitUrl: string;
    hydratedCommitDate: string;
    proposedChecks: Check[];
    activeChecks: Check[];
    prNumber: number | null;
    prUrl: string | null;
    prCreatedAt: string | null;
    mergeDate: string;
    promotionStatus: string;
    percent: number;
    autoMerge: boolean;
}
export declare function enrichPromotionStrategy(ps: PromotionStrategy): Promise<EnrichedEnvDetails[]>;
export {};
//# sourceMappingURL=PSData.d.ts.map