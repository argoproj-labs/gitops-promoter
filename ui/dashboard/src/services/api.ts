/**
 * API service for dashboard backend communication
 */

export interface MergeRequest {
  namespace: string;
  promotionStrategy: string;
  branch: string;
}

export interface MergeResponse {
  state: 'merged' | 'failed';
  message?: string;
}

/**
 * Merge a pull request for a given environment in a promotion strategy.
 * @param request - The merge request containing namespace, promotion strategy name, and branch
 * @returns The merge response indicating success or failure
 */
export async function mergePullRequest(request: MergeRequest): Promise<MergeResponse> {
  const response = await fetch('/merge', {
    method: 'POST',
    headers: {
      'Content-Type': 'application/json',
    },
    body: JSON.stringify(request),
  });

  const data = await response.json();
  
  if (!response.ok) {
    return {
      state: 'failed',
      message: data.message || `HTTP error: ${response.status}`,
    };
  }

  return data as MergeResponse;
}
