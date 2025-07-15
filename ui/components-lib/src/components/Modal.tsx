import React from 'react';
import ReactDOM from 'react-dom';
import './Modal.scss';
import { FaTimes } from 'react-icons/fa';
import TimeAgo from './TimeAgo';

interface ModalProps {
  isOpen: boolean;
  onClose: () => void;
  title?: string;
  children?: React.ReactNode;
  author?: string;
  subject?: string;
  message?: string;
  proposedSha?: string;
  commitTime?: string;
  trailers?: { [key: string]: string };
}

// Helper to remove Argocd-reference-commit-body from the main message
function removeTrailerBody(message: string | undefined, trailers: { [key: string]: string } | undefined): string {
  if (!message || !trailers) return message || '';
  const trailerBody = trailers['Argocd-reference-commit-body'];
  if (!trailerBody) return message;
  return message.replace(trailerBody, '').trim();
}

const CommitMessageModal: React.FC<ModalProps> = ({
  isOpen,
  onClose,
  title,
  author,
  subject,
  message,
  proposedSha,
  commitTime,
  trailers,
  children
}) => {
  if (!isOpen) {
    return null;
  }

  React.useEffect(() => {
    const handleKeyDown = (event: KeyboardEvent) => {
      if (event.key === 'Escape') {
        onClose();
      }
    };

    document.addEventListener('keydown', handleKeyDown);
    return () => {
      document.removeEventListener('keydown', handleKeyDown);
    };
  }, [onClose]);


  const mainMessage = removeTrailerBody(message, trailers);

  const modalContent = (
    <div className="modal-overlay" onClick={onClose}>
      <div className="modal-content" onClick={(e) => e.stopPropagation()}>
        <div className="modal-header">
          <h3 className="modal-title modal-title--black">
            {proposedSha ? `Commit ${proposedSha}` : title}
          </h3>
          <button className="modal-close-btn" onClick={onClose}>
            <FaTimes />
          </button>
        </div>
        <div className="modal-body">
          {(author || subject || message) ? (
            <div style={{ maxWidth: 600 }}>
              {subject && (
                <div className="modal-commit-subject">{subject}</div>
              )}


              {/* By <author> (authored <time ago>) */}
              {author && commitTime && (
                <div className="modal-commit-author">
                  <span>
                    by <strong>{author}</strong> (authored{' '}
                    <span title={commitTime}><TimeAgo date={commitTime} /></span>
                    )
                  </span>
                </div>
              )}
             
              {mainMessage && (
                <pre className="modal-commit-message">{mainMessage}</pre>
              )}
              {(trailers && Object.keys(trailers).length > 0) && (
                <>
                  <div className="modal-trailer-label">Trailers</div>
                  <div className="modal-trailer-box">
                    {Object.entries(trailers).map(([key, value]) => (
                      key === 'Argocd-reference-commit-body' ? (
                        <div key={key}>
                          <div style={{ fontWeight: 600, marginBottom: 4 }}>{key}:</div>
                          <pre className="modal-commit-message" style={{ margin: 0 }}>{value}</pre>
                        </div>
                      ) : (
                        <div key={key}><strong>{key}:</strong> {value}</div>
                      )
                    ))}
                  </div>
                </>
              )}
            </div>
          ) : (
            children
          )}
        </div>
      </div>
    </div>
  );

  return ReactDOM.createPortal(modalContent, document.body);
};

export default CommitMessageModal; 