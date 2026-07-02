import React from 'react';
import { FaCheck } from 'react-icons/fa';
import type { Environment } from '@shared/types/promotion';
import './EnvironmentStepper.scss';

interface EnvironmentStepperProps {
  environments: Environment[];
}

type StepState = 'complete' | 'current' | 'upcoming';

const EnvironmentStepper: React.FC<EnvironmentStepperProps> = ({ environments }) => {
  if (!environments?.length) return null;

  const states: StepState[] = (() => {
    let foundCurrent = false;
    return environments.map((env) => {
      const activeSha = env.active?.dry?.sha || env.active?.hydrated?.sha || '';
      const proposedSha = env.proposed?.dry?.sha || env.proposed?.hydrated?.sha || '';
      const isComplete = activeSha && proposedSha && activeSha === proposedSha;

      if (isComplete) return 'complete';
      if (!foundCurrent) {
        foundCurrent = true;
        return 'current';
      }
      return 'upcoming';
    });
  })();

  return (
    <div className="env-stepper" aria-label="Promotion progress">
      {environments.map((env, idx) => {
        const state = states[idx];
        const label = (env.branch || `env-${idx}`).toUpperCase();
        const circleClass = `env-stepper__circle env-stepper__circle--${state}`;

        const isLast = idx === environments.length - 1;
        const connectorComplete = state === 'complete';

        return (
          <React.Fragment key={env.branch || idx}>
            <div className={`env-stepper__step env-stepper__step--${state}`}>
              <div className={circleClass} aria-current={state === 'current' ? 'step' : undefined}>
                {state === 'complete' && <FaCheck size={12} />}
                {state === 'current' && <span className="env-stepper__dot" />}
              </div>
              <div className="env-stepper__label">{label}</div>
            </div>
            {!isLast && (
              <div
                className={`env-stepper__connector${
                  connectorComplete ? '' : ' env-stepper__connector--upcoming'
                }`}
                aria-hidden="true"
              />
            )}
          </React.Fragment>
        );
      })}
    </div>
  );
};

export default EnvironmentStepper;
