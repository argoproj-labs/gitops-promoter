// GitOps Promoter - Custom JavaScript Enhancements

// ============================================================================
// Initialize on DOM ready
// ============================================================================
document.addEventListener('DOMContentLoaded', function() {
  // Initialize all enhancements
  initSmoothScroll();
  initExternalLinks();
  initCopyCodeEnhancement();
  initTableEnhancements();
  initImageEnhancements();
  initProgressIndicator();
});

// ============================================================================
// Smooth Scroll for Anchor Links
// ============================================================================
function initSmoothScroll() {
  document.querySelectorAll('a[href^="#"]').forEach(anchor => {
    anchor.addEventListener('click', function(e) {
      const href = this.getAttribute('href');
      if (href === '#') return;

      const target = document.querySelector(href);
      if (target) {
        e.preventDefault();
        target.scrollIntoView({
          behavior: 'smooth',
          block: 'start'
        });

        // Update URL without jumping
        history.pushState(null, null, href);
      }
    });
  });
}

// ============================================================================
// External Links - Open in New Tab
// ============================================================================
function initExternalLinks() {
  document.querySelectorAll('a[href^="http"]').forEach(link => {
    // Don't modify links that already have a target
    if (!link.hasAttribute('target')) {
      // Check if link is external
      if (link.hostname !== window.location.hostname) {
        link.setAttribute('target', '_blank');
        link.setAttribute('rel', 'noopener noreferrer');

        // Add external link icon
        if (!link.querySelector('.external-link-icon')) {
          const icon = document.createElement('span');
          icon.className = 'external-link-icon';
          icon.innerHTML = ' ↗';
          icon.style.fontSize = '0.8em';
          icon.style.opacity = '0.6';
          link.appendChild(icon);
        }
      }
    }
  });
}

// ============================================================================
// Enhanced Copy Code Feedback
// ============================================================================
function initCopyCodeEnhancement() {
  // Add copy success feedback
  document.addEventListener('click', function(e) {
    if (e.target.classList.contains('md-clipboard')) {
      const button = e.target;
      const originalTitle = button.getAttribute('title');

      // Change title temporarily
      button.setAttribute('title', 'Copied! ✓');
      button.style.color = '#4caf50';

      // Reset after 2 seconds
      setTimeout(() => {
        button.setAttribute('title', originalTitle || 'Copy to clipboard');
        button.style.color = '';
      }, 2000);
    }
  });
}

// ============================================================================
// Table Enhancements - Make Tables Responsive
// ============================================================================
function initTableEnhancements() {
  document.querySelectorAll('.md-typeset table').forEach(table => {
    // Wrap table in responsive container if not already wrapped
    if (!table.parentElement.classList.contains('table-wrapper')) {
      const wrapper = document.createElement('div');
      wrapper.className = 'table-wrapper';
      wrapper.style.overflowX = 'auto';
      wrapper.style.marginBottom = '1.5em';
      table.parentNode.insertBefore(wrapper, table);
      wrapper.appendChild(table);
    }

    // Add sortable functionality hint to headers
    const headers = table.querySelectorAll('th');
    headers.forEach(header => {
      if (!header.classList.contains('no-sort')) {
        header.style.cursor = 'pointer';
        header.title = 'Click to sort';
      }
    });
  });
}

// ============================================================================
// Image Enhancements - Add Lightbox and Zoom
// ============================================================================
function initImageEnhancements() {
  document.querySelectorAll('.md-typeset img').forEach(img => {
    // Skip if already wrapped
    if (img.parentElement.classList.contains('img-wrapper')) return;

    // Add loading animation
    img.style.transition = 'opacity 0.3s ease';
    img.style.opacity = '0';

    img.addEventListener('load', function() {
      img.style.opacity = '1';
    });

    // Add click to zoom functionality for larger images
    if (img.naturalWidth > 600) {
      img.style.cursor = 'pointer';
      img.title = 'Click to enlarge';

      img.addEventListener('click', function() {
        // Create modal for full-size image
        const modal = document.createElement('div');
        modal.style.cssText = `
          position: fixed;
          top: 0;
          left: 0;
          width: 100%;
          height: 100%;
          background: rgba(0, 0, 0, 0.9);
          display: flex;
          align-items: center;
          justify-content: center;
          z-index: 10000;
          cursor: pointer;
        `;

        const modalImg = document.createElement('img');
        modalImg.src = img.src;
        modalImg.style.cssText = `
          max-width: 90%;
          max-height: 90%;
          object-fit: contain;
          border-radius: 8px;
          box-shadow: 0 8px 32px rgba(0, 0, 0, 0.5);
        `;

        modal.appendChild(modalImg);
        document.body.appendChild(modal);

        // Close modal on click
        modal.addEventListener('click', function() {
          document.body.removeChild(modal);
        });

        // Close on escape key
        document.addEventListener('keydown', function escapeHandler(e) {
          if (e.key === 'Escape') {
            if (document.body.contains(modal)) {
              document.body.removeChild(modal);
            }
            document.removeEventListener('keydown', escapeHandler);
          }
        });
      });
    }
  });
}

// ============================================================================
// Reading Progress Indicator
// ============================================================================
function initProgressIndicator() {
  // Create progress bar element
  const progressBar = document.createElement('div');
  progressBar.id = 'reading-progress';
  progressBar.style.cssText = `
    position: fixed;
    top: 0;
    left: 0;
    width: 0%;
    height: 3px;
    background: linear-gradient(90deg, #00897b, #ff6e40);
    z-index: 10000;
    transition: width 0.1s ease;
  `;
  document.body.appendChild(progressBar);

  // Update progress on scroll
  window.addEventListener('scroll', function() {
    const windowHeight = window.innerHeight;
    const documentHeight = document.documentElement.scrollHeight - windowHeight;
    const scrollTop = window.pageYOffset || document.documentElement.scrollTop;
    const scrollPercentage = (scrollTop / documentHeight) * 100;

    progressBar.style.width = scrollPercentage + '%';
  });
}

// ============================================================================
// Add Copy Button to Non-Code Pre Elements
// ============================================================================
function addCopyButtons() {
  document.querySelectorAll('pre').forEach(pre => {
    // Skip if already has a copy button
    if (pre.querySelector('.custom-copy-btn')) return;

    const button = document.createElement('button');
    button.className = 'custom-copy-btn';
    button.innerHTML = '📋';
    button.title = 'Copy to clipboard';
    button.style.cssText = `
      position: absolute;
      top: 0.5em;
      right: 0.5em;
      padding: 0.3em 0.6em;
      background: var(--md-primary-fg-color);
      color: var(--md-primary-bg-color);
      border: none;
      border-radius: 4px;
      cursor: pointer;
      font-size: 0.9em;
      opacity: 0.7;
      transition: opacity 0.2s;
    `;

    button.addEventListener('mouseenter', function() {
      button.style.opacity = '1';
    });

    button.addEventListener('mouseleave', function() {
      button.style.opacity = '0.7';
    });

    button.addEventListener('click', function() {
      const text = pre.textContent;
      navigator.clipboard.writeText(text).then(() => {
        button.innerHTML = '✓';
        button.style.background = '#4caf50';
        setTimeout(() => {
          button.innerHTML = '📋';
          button.style.background = 'var(--md-primary-fg-color)';
        }, 2000);
      });
    });

    pre.style.position = 'relative';
    pre.appendChild(button);
  });
}

// ============================================================================
// Keyboard Shortcuts
// ============================================================================
document.addEventListener('keydown', function(e) {
  // Ctrl/Cmd + K to focus search
  if ((e.ctrlKey || e.metaKey) && e.key === 'k') {
    e.preventDefault();
    const searchInput = document.querySelector('.md-search__input');
    if (searchInput) {
      searchInput.focus();
    }
  }

  // Escape to close search
  if (e.key === 'Escape') {
    const searchInput = document.querySelector('.md-search__input');
    if (searchInput && document.activeElement === searchInput) {
      searchInput.blur();
    }
  }
});

// ============================================================================
// Add Tooltips to Abbreviations
// ============================================================================
function initAbbreviations() {
  document.querySelectorAll('abbr[title]').forEach(abbr => {
    abbr.style.cursor = 'help';
    abbr.style.textDecoration = 'underline dotted';
  });
}

// ============================================================================
// Performance: Lazy Load Images
// ============================================================================
if ('IntersectionObserver' in window) {
  const imageObserver = new IntersectionObserver((entries, observer) => {
    entries.forEach(entry => {
      if (entry.isIntersecting) {
        const img = entry.target;
        if (img.dataset.src) {
          img.src = img.dataset.src;
          img.removeAttribute('data-src');
          imageObserver.unobserve(img);
        }
      }
    });
  });

  // Observe images with data-src attribute
  document.querySelectorAll('img[data-src]').forEach(img => {
    imageObserver.observe(img);
  });
}

// ============================================================================
// Console Easter Egg
// ============================================================================
console.log('%c GitOps Promoter ', 'background: #00897b; color: white; font-size: 20px; font-weight: bold; padding: 10px;');
console.log('%c A GitOps First Environment Promotion Tool ', 'font-size: 12px; color: #00897b;');
console.log('%c GitHub: https://github.com/argoproj-labs/gitops-promoter ', 'font-size: 10px; color: #666;');
console.log('%c ⭐ Star us on GitHub if you find this useful! ', 'font-size: 12px; color: #ff6e40; font-weight: bold;');
