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

export function PromotionStrategiesListUpdate() {
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

        const eventSource = new EventSource("/watch?kind=promotionstrategy");

        // Define event handlers before attaching them
        eventSource.addEventListener("PromotionStrategy", event => {
            try {
                const message = JSON.parse(event.data);
                console.log("Received SSE message:", message);

                setStrategies((currentStrategies: any[]) => {
                    // Find if the policy already exists in our list
                    const index = currentStrategies.findIndex(
                        (p) =>
                            p.metadata.name === message.metadata.name &&
                            p.metadata.namespace === message.metadata.namespace
                    );

                    // Create a new array to trigger a re-render
                    const updatedStrategies = [...currentStrategies];

                    // Add or update the policy
                    if (index >= 0) {
                        updatedStrategies[index] = message;
                    } else {
                        updatedStrategies.push(message);
                    }

                    return updatedStrategies;
                });
            } catch (err: any) {
                console.error("Error processing SSE message:", err);
            }
        });

        // Add error and open handlers to help debug connection issues
        eventSource.onerror = (error) => {
            console.error("EventSource error:", error);
        };

        eventSource.onopen = () => {
            console.log("EventSource connection opened");
        };

        // Clean up function to properly close connection when component unmounts
        return () => {
            console.log("Closing EventSource connection");
            eventSource.close();
        };

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
                        <p className="text-gray-600">
                            Status: <br /> {strategy.status?.environments ? strategy.status.environments.map((env, index) => (
                            <span key={index} className="ml-4">
                      • {env.branch}:{env.proposed.dry.sha == env.active.dry.sha ? "(closed pr)" : "(open pr)"}  <br/>
                                &nbsp;&nbsp;&nbsp;&nbsp;&nbsp;&nbsp;• Active: {env.active.commitStatus.phase} <br/>
                                &nbsp;&nbsp;&nbsp;&nbsp;&nbsp;&nbsp;&nbsp;&nbsp;• sha: {env.active.commitStatus.sha} <br/>
                                &nbsp;&nbsp;&nbsp;&nbsp;&nbsp;&nbsp;&nbsp;&nbsp;• dry sha: {env.active.dry.sha} <br/>
                                &nbsp;&nbsp;&nbsp;&nbsp;&nbsp;&nbsp;• Proposed: {env.proposed.commitStatus.phase} <br/>
                                &nbsp;&nbsp;&nbsp;&nbsp;&nbsp;&nbsp;&nbsp;&nbsp;• sha: {env.proposed.commitStatus.sha} <br/>
                                &nbsp;&nbsp;&nbsp;&nbsp;&nbsp;&nbsp;&nbsp;&nbsp;• dry sha: {env.proposed.dry.sha}  <br/><br/>
                    </span>
                        )) : 'No status available'}
                        </p>
                    </li>
                ))}
            </ul>
        </div>
    );
}