import React from 'react';
import { enrichPromotionStrategy } from '@shared/utils/PSData';
import EnvironmentCard from '../components-lib/dist/components/EnvironmentCard';
import './index.scss';
const Extension = ({ resource }) => {
    const [enrichedEnvs, setEnrichedEnvs] = React.useState([]);
    const [isLoading, setIsLoading] = React.useState(true);
    React.useEffect(() => {
        //Checks if data is loaded and sets the state for the component
        let isMounted = true;
        async function enrich() {
            if (resource) {
                const enriched = await enrichPromotionStrategy(resource);
                if (isMounted) {
                    setEnrichedEnvs(enriched);
                    setIsLoading(false);
                }
            }
        }
        enrich();
        return () => {
            isMounted = false;
        };
    }, [resource]);
    return (React.createElement("div", { className: "extension-container" }, isLoading ? (React.createElement("div", { className: "loading-indicator" }, "Loading environment details...")) : (React.createElement(EnvironmentCard, { environments: enrichedEnvs }))));
};
// Register extension
window.extensionsAPI?.registerResourceExtension(Extension, 'promoter.argoproj.io', 'PromotionStrategy', 'PromotionStrategy', { icon: 'fa-code-branch' });
