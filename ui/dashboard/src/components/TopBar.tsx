import React from 'react';
import { Link, useLocation } from 'react-router';
import { FaMoon, FaSun } from 'react-icons/fa';
import { carriedSearch } from '../hooks/useNavigateWithParams';
import { themeStore } from '../stores/ThemeStore';
import logoSecondary from '../assets/logo-one-row-secondary.svg';
import './TopBar.scss';

export const TopBar: React.FC = () => {
  const search = carriedSearch(useLocation().search);
  const { theme, toggle } = themeStore();
  const label = theme === 'dark' ? 'Switch to light mode' : 'Switch to dark mode';
  return (
    <header className="topbar">
      <Link to={{ pathname: '/promotion-strategies', search }} className="topbar__brand">
        <img src={logoSecondary} alt="GitOps Promoter" className="topbar__logo" />
      </Link>
      <div className="topbar__center">Promotion Strategies</div>
      <button
        type="button"
        className="topbar__theme"
        onClick={toggle}
        aria-label={label}
        title={label}
      >
        {theme === 'dark' ? <FaSun /> : <FaMoon />}
      </button>
    </header>
  );
};
