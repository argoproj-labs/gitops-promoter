export declare const timeAgo: (dateString: string) => string;
export declare function extractEnvNameFromBranch(branch: string): string;
export declare function getCommitUrl(repoUrl: string, sha: string): string;
export declare function extractNameOnly(author: string): string;
export declare function extractBodyPreTrailer(body: string): string;
export declare function parseTrailers(body: string): {
    trailers: {
        [key: string]: string;
    };
    trailerBody?: string;
};
export declare function getArgoCDAppLink(appName: string, namespace?: string, baseUrl?: string): string;
export declare function getAppsForEnvironment(apps: any[], env: string): any[];
export declare function formatDate(date?: string): string;
//# sourceMappingURL=util.d.ts.map