import { FaChevronLeft } from 'react-icons/fa';

const BackButton: React.FC<{ onClick: () => void }> = ({ onClick }) => (
    <div style={{ display: 'flex',  marginBottom: 16, width: '100%', padding: '15px 16px 0px 16px', backgroundColor: 'none' }}>
      <button
        onClick={onClick}
        style={{ background: 'none', border: 'none', cursor: 'pointer', padding: 0, display: 'flex', alignItems: 'center' }}
        aria-label="Back"
      >
        <FaChevronLeft size={22} />
        <span style={{ fontWeight: 600, fontSize: 18, marginLeft: 8 }}>Back</span>
      </button>
    </div>
  );

export default BackButton