import React from 'react';
import { Link } from 'react-router-dom';
import logoSecondary from '../assets/logo-one-row-secondary.svg';
import './TopBar.scss';

export const TopBar: React.FC = () => (
  <header className="topbar">
    <Link to="/" className="topbar__brand">
      <img src={logoSecondary} alt="GitOps Promoter" className="topbar__logo" />
    </Link>
    <div className="topbar__center">Promotion Strategies</div>
  </header>
);
