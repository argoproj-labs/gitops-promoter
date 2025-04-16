import { useEffect, useState } from "react";

interface PromotionStrategy {
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

export function PromotionStrategiesList() {
    const [strategies, setStrategies] = useState<PromotionStrategy[]>([]);
    const [error, setError] = useState<string | null>(null);

    useEffect(() => {
        async function fetchStrategies() {
            try {
                const response = await fetch("/list?kind=promotionstrategy");
                if (!response.ok) {
                    throw new Error(`Error: ${response.statusText}`);
                }
                const data = await response.json();
                setStrategies(data); // Assuming the API returns a `PromotionStrategyList` object
            } catch (err: any) {
                setError(err.message);
            }
        }

        fetchStrategies();
    }, []);

    if (error) {
        return <div>Error: {error}</div>;
    }

    return (
        <div className="promotion-strategies">
            <h2 className="text-xl font-bold mb-4">Promotion Strategies</h2>
            <ul className="space-y-2">
                {strategies.map((strategy) => (
                    <li key={strategy.metadata.namespace + "/" + strategy.metadata.name} className="p-4 border rounded shadow">
                        <h3 className="text-lg font-semibold">{strategy.metadata.namespace + "/" + strategy.metadata.name}</h3>
                        <p className="text-gray-600">
                            Repository: {strategy.spec.gitRepositoryRef.name}
                        </p>
                        <p className="text-gray-600">
                            Environment Count: {strategy.spec.environments.length}
                        </p>
                        <p className="text-gray-600">
                            Environments: <br />{strategy.spec.environments.map((env, index) => (
                            <span key={index} className="ml-4">
                      • {env.branch} {env.autoMerge ? "(Auto Merge)" : ""}<br />
                    </span>
                        ))}
                        </p>
                    </li>
                ))}
            </ul>
        </div>
    );
}