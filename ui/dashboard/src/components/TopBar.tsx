import React from 'react';
import { Link } from 'react-router-dom';
import argoIcon from '../assets/argo-icon-color-square.png';
import argoTextLogo from '../assets/argologo.svg';
import './TopBar.scss';

export const TopBar: React.FC = () => (
    <header className='topbar'>
        <Link to='/' className='topbar__brand'>
            <img src={argoIcon} alt='Argo Icon' className='topbar__logo' />
            <div>
                <div className='topbar__title'>
                    <img src={argoTextLogo} style={{ filter: 'invert(100%)', height: '1em' }} />
                </div>
                <div className='topbar__label'>GitOps Promoter</div>
            </div>
        </Link>
        <div className='topbar__center'>Promotion Strategies</div>
    </header>
);