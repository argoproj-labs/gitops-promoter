import { FaServer, FaExpandAlt } from 'react-icons/fa';
import { StatusIcon, statusLabel } from './StatusIcon';
import './EnvironmentCard.scss';
import PrCard from './PRCard';
import React, { useState} from 'react';
import CommitMessageModal from './CommitMessageModal';

//Props: 
//Environments: For active (dry/hydrated) branches and commit messages/urls
//gitRepositories: array of objects with metadata.name, spec.github.owner, spec.github.name, spec.repoURL
export interface EnvironmentCardProps {
  environments: any[];
}

const EnvironmentCard: React.FC<EnvironmentCardProps> = ({ environments }) => {
  const [isMessageModalOpen, setMessageModalOpen] = useState(false);
  const [modalInfo, setModalInfo] = useState<any | null>(null);

  const openMessageModal = (info: any) => {
    setModalInfo(info);
    setMessageModalOpen(true);
  };

  const closeMessageModal = () => {
    setMessageModalOpen(false);
    setModalInfo(null);
  };

  return (
    <div className="env-cards-container">
      {environments.map((env: any, envIdx: number) => {
        const branch = env.branch;
        const phase = env.phase;
        const lastSync = env.lastSync;
        const dryCommitAuthor = env.dryCommitAuthor;
        const dryCommitMessage = env.dryCommitMessage;
        const dryCommitSubject = env.dryCommitSubject;
        const activeChecks = env.activeChecks;
        const drySha = env.drySha;
        const dryCommitUrl = env.dryCommitUrl;


        // Only render PR card if not 'success'
        let prCard: React.ReactNode = null;
        if (env.promotionStatus !== 'success') {
          let autoExpand = false;
          if (env.promotionStatus === 'promoted') {
            autoExpand = true;
          } else if (env.promotionStatus === 'pending') {
            autoExpand = false;
          }

          prCard = (
            <PrCard
              autoExpand={autoExpand}
              commit={{
                hydratedSha: env.hydratedSha,
                hydratedCommitUrl: env.hydratedCommitUrl,
                proposedDryCommitAuthor: env.proposedDryCommitAuthor,
                proposedDryCommitSubject: env.proposedDryCommitSubject,
                proposedDryCommitBody: env.proposedDryCommitBody,
                proposedDryCommitUrl: env.proposedDryCommitUrl,
                proposedDryCommitDate: env.proposedDryCommitDate,
                branch: env.branch,
                link: env.proposedDryCommitUrl,
                author: env.proposedDryCommitAuthor,
                message: env.proposedDryCommitSubject,
                body: env.proposedDryCommitBody,
                checks: env.proposedChecks,
                proposedSha: env.proposedSha,
                prCreatedAt: env.prCreatedAt,
                prNumber: env.prNumber,
                mergeDate: env.mergeDate,
                promotionStatus: env.promotionStatus,
                percent: env.percent,
                prUrl: env.prUrl,
              }}
            />
          );
        }


        return (
          <div key={env.branch} className="env-card-column">
            <div className="env-card">
              <div className="env-card__header" style={{ display: 'flex', alignItems: 'center' }}>
                <FaServer className="env-card__icon" />
                <span className="env-card__env-name">{branch}</span>
              </div>
              
              <div className="env-card__status-row">
                <StatusIcon phase={phase} type="health" />
                <span className={`env-card__status-label env-card__status-label--${phase}`}>{statusLabel(phase)}</span>
              </div>

              {/* ENVIRONMENT DETAILS */}
              <div className="card-field-group">
                <div className="card-row">
                  <div className="card-label">Active Commit:</div>
                  <div className="card-value">
                    {drySha !== '-' && dryCommitUrl !== '-' ? (
                      <a className="card-value-field" href={dryCommitUrl} target="_blank" rel="noopener noreferrer">
                        {drySha}
                      </a>
                    ) : (
                      <span className="card-value-field">{drySha}</span>
                    )}
                  </div>
                </div>
                <div className="card-row">
                  <div className="card-label">Author:</div>
                  <div className="card-value">
                    <span className="card-value-field">{dryCommitAuthor}</span>
                  </div>
                </div>
                <div className="card-row card-row--message">
                  <div className="card-label">Message:</div>
                  <div className="card-value card-value--with-expand">
                    <span className="card-value-field card-value-field--truncate">{dryCommitSubject}</span>
                    {dryCommitMessage && dryCommitMessage !== dryCommitSubject && (
                      <span className="card-expand-icon-wrapper">
                        <FaExpandAlt
                          className="card-expand-icon"
                          onClick={() => openMessageModal({
                            proposedSha: env.proposedSha,
                            author: env.dryCommitAuthor,
                            commitTime: env.dryCommitDate,
                            subject: env.dryCommitSubject,
                            message: env.dryCommitMessage,
                            trailers: env.dryCommitTrailers,
                            trailerBody: env.dryCommitTrailerBody,
                          })}

                          
                          title="View full message"
                        />
                      </span>
                    )}
                  </div>
                </div>
                <div className="card-row">
                  <div className="card-label">Last Sync:</div>
                  <div className="card-value">
                    <span className="card-value-field">{lastSync}</span>
                  </div>
                </div>


                {/* ACTIVE CHECKS */}
                {activeChecks.length > 0 && (
                  <div className="card-checks-list">
                    <div className="card-row">
                      <div className="card-label">Active Checks:</div>
                    </div>
                    <ul>
                      {activeChecks.map((check: any, idx: number) => (
                        <li key={check.name + '-' + idx} className="card-check-item">
                          <span className="card-check-icon">
                            <StatusIcon phase={check.status} type="status" />
                          </span>
                          <span className="card-check-name">{check.name}</span>
                          {check.url && (
                            <a href={check.url} className="card-check-link" target="_blank" rel="noopener noreferrer">
                              View details
                            </a>
                          )}
                          {check.details && (
                            <a href={check.details} className="card-check-link" target="_blank" rel="noopener noreferrer">View details</a>
                          )}
                        </li>
                      ))}
                    </ul>
                  </div>
                )}
              </div>
            </div>
            {prCard}
          </div>
        );
      })}


      {/* Commit Message Popup */}
      <CommitMessageModal
        isOpen={isMessageModalOpen}
        onClose={closeMessageModal}
        proposedSha={modalInfo?.proposedSha}
        author={modalInfo?.author}
        commitTime={modalInfo?.commitTime}
        subject={modalInfo?.subject}
        message={modalInfo?.message}
        trailers={modalInfo?.trailers}
        trailerBody={modalInfo?.trailerBody}
      />
    </div>
  );
};

export default EnvironmentCard;