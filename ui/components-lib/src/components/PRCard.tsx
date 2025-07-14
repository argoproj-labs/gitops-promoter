import React, { useEffect, useState, useRef } from 'react';
import './PRCard.scss';
import CircularProgress from './CircularProgress';
import { StatusIcon } from './StatusIcon';
import { GoGitPullRequest } from 'react-icons/go';
import { FiChevronDown, FiChevronUp } from 'react-icons/fi';
import { FaExpandAlt } from 'react-icons/fa';
import CommitMessageModal from './CommitMessageModal';
import TimeAgo from './TimeAgo';

interface ProposedCheck {
  name: string;
  status: string;
  details: string;
  detailLinks?: string;
  url?: string;
}

interface PRDetails {
  hydratedSha: string;
  hydratedCommitUrl?: string;
  proposedDryCommitAuthor: string;
  proposedDryCommitSubject: string;
  proposedDryCommitBody?: string;
  proposedDryCommitUrl?: string;
  proposedDryCommitDate?: string;
  branch: string;
  link: string;
  author: string;
  message: string;
  body?: string;
  checks: ProposedCheck[];
  proposedSha?: string;
  prCreatedAt?: string; 
  prNumber?: number;
  mergeDate?: string;
  promotionStatus: string;
  percent: number;
  prUrl?: string;
}

interface PRProps {
  autoExpand: boolean;
  commit: PRDetails;
}

const PrCard = ({ autoExpand, commit }: PRProps) => {


  
  let progressLabel = '';
  let iconType: 'pr' | 'progress' = 'progress';
  const percent = commit.percent;

  if (commit.promotionStatus === 'pending') {
    progressLabel = 'Opened';
    iconType = 'pr';
  } else if (commit.promotionStatus === 'promoted') {
    iconType = 'progress';
  } else if (commit.promotionStatus === 'success') {
    iconType = 'progress';
  } else if (commit.promotionStatus === 'failure') {
    progressLabel = 'Failed';
    iconType = 'progress';
  } else {
    iconType = 'progress';
  }

  const [expanded, setExpanded] = useState(autoExpand);
  const prevPromotionStatus = useRef<string>(commit.promotionStatus);
  const [isMessageModalOpen, setMessageModalOpen] = useState(false);
  const [modalInfo, setModalInfo] = useState<any | null>(null);

  useEffect(() => {
    if (commit.promotionStatus === 'success' && prevPromotionStatus.current !== 'success') {
      setExpanded(false);
    }
    prevPromotionStatus.current = commit.promotionStatus;
  }, [commit.promotionStatus]);



  // When autoExpand changes, update expanded state
  useEffect(() => {
    setExpanded(autoExpand);
  }, [autoExpand]);

  
  // Remove the card when promotionStatus is 'success'
  if ((commit.promotionStatus as string) === 'success') return null;

  const openMessageModal = () => {
    setModalInfo({
      proposedSha: commit.proposedSha,
      author: commit.proposedDryCommitAuthor,
      commitTime: commit.proposedDryCommitDate,
      subject: commit.proposedDryCommitSubject,
      message: commit.proposedDryCommitBody,
    });
    setMessageModalOpen(true);
  };

  const closeMessageModal = () => {
    setMessageModalOpen(false);
    setModalInfo(null);
  };

  return (
    <div className={`pr-card pr-card--${commit.promotionStatus}`}>
      
      {/* Progress Bar or PR Icon */}
      <div className="pr-card__header">
        <div style={{ position: 'relative', display: 'inline-block', width: 26, height: 26 }}>
          {iconType === 'pr' ? (
            <a href={commit.prUrl} target="_blank" rel="noopener noreferrer"
            className="pr-card__icon">
              <div className="pr-card__pr-icon-text">
                <GoGitPullRequest />
                <span className="pr-card__pr-icon-text">#{commit.prNumber}</span>
              </div>
            </a>
          ) : (
            <div className="pr-card__progress">
            <CircularProgress percent={percent}/>
            </div>
          )}
        </div>


        {/* CARD*/}
        <span>
          Desired SHA -&gt; {commit.proposedSha}
        </span>
        <span
          className="pr-card__toggle"
          style={{ cursor: 'pointer', position: 'absolute', right: 12, top: 12 }}
          onClick={() => setExpanded((prev) => !prev)}
          title={expanded ? 'Collapse' : 'Expand'}
        >
          {expanded ? <FiChevronUp /> : <FiChevronDown />}
        </span>
      </div>
      <div className="card__progress-label">
        {progressLabel}
        {commit.promotionStatus === 'promoted' || commit.promotionStatus === 'success' ? (
          commit.mergeDate && (
            <> Promoted (<span title={commit.mergeDate}><TimeAgo date={commit.mergeDate} /></span>)</>
          )
        ) : commit.promotionStatus === 'pending' && commit.prCreatedAt ? (
          <> (<span title={commit.prCreatedAt}><TimeAgo date={commit.prCreatedAt} /></span>)</>
        ) : null}
      </div>



      {expanded && (
        <div className="pr-card__body">
          {/* {commit.prNumber && commit.prUrl && commit.promotionStatus !== 'promoted' && (
            <div className="card-row">
              <div className="card-label">Pull Request:</div>
              <div className="card-value">
                <a
                  href={commit.prUrl}
                  target="_blank"
                  rel="noopener noreferrer"
                  title={`View PR #${commit.prNumber}`}
                  className="card-value-field"
                >
                  PR #{commit.prNumber}
                </a>
              </div>
            </div>
          )} */}
          <div className="card-row">
            <div className="card-label">Hydrated Commit:</div>
            <div className="card-value">
              <a
                href={commit.hydratedCommitUrl}
                target="_blank"
                rel="noopener noreferrer"
                title={commit.hydratedSha}
                className="card-value-field"
              >
                {commit.hydratedSha}
              </a>
            </div>
          </div>
          <div className="card-row">
            <div className="card-label">Author:</div>
            <div className="card-value">
              {commit.proposedDryCommitAuthor}
            </div>
          </div>
          <div className="card-row">
            <div className="card-label">Message:</div>
            <div className="card-value card-value--with-expand">
              <span className="card-value-field card-value-field--truncate">{commit.proposedDryCommitSubject}</span>
              {commit.proposedDryCommitBody && commit.proposedDryCommitBody !== commit.proposedDryCommitSubject && (
                <span className="card-expand-icon-wrapper">
                  <FaExpandAlt
                    className="card-expand-icon"
                    onClick={openMessageModal}
                    title="View full message"
                  />
                </span>
              )}
            </div>
          </div>


          
          {Array.isArray(commit.checks) && commit.checks.length > 0 && (
            <div className="card__checks-list">
              <div className="card-row">
                <div className="card-label">Checks:</div>
              </div>
              <ul>
                {commit.checks.map((check, idx) => (
                  <li key={idx} className="card-check-item">
                    <span className="card-check-icon">
                      <StatusIcon phase={check.status as any} type="status" />
                    </span>
                    <span className="card-check-name">{check.name}</span>
                    <span style={{ marginLeft: 'auto', display: 'flex', alignItems: 'center', gap: 8 }}>
                      {check.url && (
                        <a href={check.url} className="card-check-link" target="_blank" rel="noopener noreferrer">
                          View details
                        </a>
                      )}
                      {check.details && (
                        <a href={check.details} className="card-check-link" target="_blank" rel="noopener noreferrer">View details</a>
                      )}
                      {check.detailLinks && (
                        <a
                          href={check.detailLinks}
                          className="card-check-link"
                          target="_blank"
                          rel="noopener noreferrer"
                        >
                          View Details
                        </a>
                      )}
                    </span>
                  </li>
                ))}
              </ul>
            </div>
          )}
        </div>
      )}
      <CommitMessageModal
        isOpen={isMessageModalOpen}
        onClose={closeMessageModal}
        proposedSha={modalInfo?.proposedSha}
        author={modalInfo?.author}
        commitTime={modalInfo?.commitTime}
        subject={modalInfo?.subject}
        message={modalInfo?.message}
      />
    </div>
  );
};
export default PrCard; 