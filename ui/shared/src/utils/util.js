//Get duration ago (E.g: 1 day ago)
export const timeAgo = (dateString) => {
    const now = new Date();
    const date = new Date(dateString);
    const diffMs = now.getTime() - date.getTime();
    const diffSeconds = Math.floor(diffMs / 1000);
    const diffMinutes = Math.floor(diffSeconds / 60);
    const diffHours = Math.floor(diffMinutes / 60);
    const diffDays = Math.floor(diffHours / 24);
    if (diffSeconds < 60) {
        return `${diffSeconds <= 1 ? 1 : diffSeconds} second${diffSeconds === 1 ? '' : 's'} ago`;
    }
    else if (diffMinutes < 60) {
        return `${diffMinutes} minute${diffMinutes === 1 ? '' : 's'} ago`;
    }
    else if (diffHours < 24) {
        return `${diffHours} hour${diffHours === 1 ? '' : 's'} ago`;
    }
    else {
        return `${diffDays} day${diffDays === 1 ? '' : 's'} ago`;
    }
};
//Extract environment name -> remove leading prefixes ending with / or -
export function extractEnvNameFromBranch(branch) {
    const env = branch.replace(/^([a-zA-Z0-9_-]+[\/-])+/, '').trim().toLowerCase();
    return env || branch;
}
// Get the commit url from the repo url and sha
export function getCommitUrl(repoUrl, sha) {
    if (!repoUrl || !sha)
        return '';
    const cleanRepoUrl = repoUrl.replace(/\/$/, '');
    return `${cleanRepoUrl}/commit/${sha}`;
}
//Extract name from 'Name <email>'
export function extractNameOnly(author) {
    const match = author.match(/^([^<]+)</);
    if (match)
        return match[1].trim();
    return author;
}
//Extracts the body before trailers
export function extractBodyPreTrailer(body) {
    if (!body)
        return '';
    const lines = body.split(/\r?\n/);
    const trailerStart = lines.findIndex(line => /^([A-Za-z0-9-]+:|Signed-off-by:)/.test(line.trim()));
    if (trailerStart === -1)
        return body.trim();
    return lines.slice(0, trailerStart).join('\n').trim();
}
// Parse trailers and trailer body from a commit message
export function parseTrailers(body) {
    if (!body)
        return { trailers: {} };
    const lines = body.split(/\r?\n/);
    const trailers = {};
    let inTrailers = false;
    for (const line of lines) {
        if (/^([A-Za-z0-9-]+:|Signed-off-by:)/.test(line.trim())) {
            inTrailers = true;
        }
        if (inTrailers) {
            const match = line.match(/^([A-Za-z0-9-]+):\s*(.*)$/);
            if (match) {
                trailers[match[1]] = match[2];
            }
        }
    }
    const trailerBody = trailers['Argocd-reference-commit-body'];
    return { trailers, trailerBody };
}
//Get ArgoCD application link
export function getArgoCDAppLink(appName, namespace = 'argocd', baseUrl = 'https://localhost:8080') {
    return `${baseUrl}/applications/${namespace}/${appName}?view=tree&resource=`;
}
export function getAppsForEnvironment(apps, env) {
    if (!env)
        return [];
    return apps.filter(app => app.name === env ||
        app.name.endsWith(`-${env}`) ||
        app.name.startsWith(`${env}-`) ||
        app.name.includes(env));
}
// date formatting (e.g: Jul 5 2025, 12:15pm EDT)
export function formatDate(date) {
    if (!date)
        return '-';
    const d = new Date(date);
    return d.toLocaleString(undefined, {
        month: 'short',
        day: 'numeric',
        year: 'numeric',
        hour: 'numeric',
        minute: '2-digit',
        hour12: true,
        timeZoneName: 'short'
    }).replace(',', '').replace(/:00 /, ' '); // Remove seconds if present
}
