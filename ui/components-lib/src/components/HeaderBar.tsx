import React from 'react';

interface Props {
  name: string;
  link?: string;
}

const HeaderBar: React.FC<Props> = ({ name, link }) => {
  const content = (
    <h2 style={{ margin: 0, fontSize: 20, fontWeight: 600 }}>{name}</h2>
  );

  return (
    <div style={{ width: '100%', display: 'flex', justifyContent: 'center', alignItems: 'center', padding: '16px 0', backgroundColor: 'white', cursor: link ? 'pointer' : 'default' }}>
      {link ? (
        <a href={link} style={{ textDecoration: 'none', color: 'inherit', width: '100%', display: 'flex', justifyContent: 'center' }}>
          {content}
        </a>
      ) : (
        content
      )}
    </div>
  );
};

export default HeaderBar; 