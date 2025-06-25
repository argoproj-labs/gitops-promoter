import { useEffect, useState } from "react";
import { PromotionStrategyType } from "../../models/promotionstrategy.tsx";

export function PromotionStrategy(props: {ps: PromotionStrategyType}) {
    const [strategy, setStrategy] = useState<PromotionStrategyType>();
    //const [error, setError] = useState<string | null>(null);

    useEffect(() => {
        setStrategy(props.ps);
    }, [props.ps]);

    return (
        <div className="promotion-strategies">
            <h2 className="text-xl font-bold mb-4">Promotion Strategies</h2>
            <ul className="space-y-2">
                <li key={strategy?.metadata.namespace + "/" + strategy?.metadata.name} className="p-4 border rounded shadow">
                    <h3 className="text-lg font-semibold">{strategy?.metadata.namespace + "/" + strategy?.metadata.name}</h3>
                    <p className="text-gray-600">
                        Repository: {strategy?.spec.gitRepositoryRef.name}
                    </p>
                    <p className="text-gray-600">
                        Environment Count: {strategy?.spec.environments.length}
                    </p>
                    <p className="text-gray-600">
                        Annotations: <br />{strategy?.metadata.annotations ?
                        Object.entries(strategy?.metadata.annotations).map(([key, value]) =>
                                <span key={key} className="ml-4">
                          •  {key}: {value} <br />
                        </span>
                        ) : 'None'}
                    </p>
                    <p className="text-gray-600">
                        Environments: <br />{strategy?.spec.environments.map((env, index) => (
                        <span key={index} className="ml-4">
                  • {env.branch} {env.autoMerge ? "(Auto Merge)" : ""}<br />
                </span>
                    ))}
                    </p>
                    <p className="text-gray-600">
                        Status: <br /> {strategy?.status?.environments ? strategy?.status.environments.map((env, index) => (
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
            </ul>
        </div>
    );
}