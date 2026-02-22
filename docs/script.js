// Theme toggle
function toggleTheme() {
  const html = document.documentElement;
  const btn = document.querySelector('.theme-toggle');
  if (html.getAttribute('data-theme') === 'dark') {
    html.removeAttribute('data-theme');
    btn.textContent = '🌙';
    localStorage.setItem('theme', 'light');
  } else {
    html.setAttribute('data-theme', 'dark');
    btn.textContent = '☀️';
    localStorage.setItem('theme', 'dark');
  }
}

// Load theme: saved preference > system preference > light
(function() {
  const saved = localStorage.getItem('theme');
  const btn = document.querySelector('.theme-toggle');
  const prefersDark = window.matchMedia('(prefers-color-scheme: dark)').matches;
  
  const useDark = saved === 'dark' || (saved === null && prefersDark);
  if (useDark) {
    document.documentElement.setAttribute('data-theme', 'dark');
    btn.textContent = '☀️';
  }
})();

// Floating emojis - symbolizing global conversation
(function() {
  // Each emoji has a native direction (how it looks without flipping)
  // All emojis can go both directions; flip when going opposite to native
  const emojiConfig = [
    { char: '💬', native: 'ltr' },  // tail points left → native LTR
    { char: '🗨️', native: 'rtl' },  // tail points left → native RTL
    { char: '🗯️', native: 'ltr' },  // tail points left → native LTR
    { char: '💭', native: 'ltr' },  // tail points left → native LTR
    { char: '📨', native: 'ltr' },  // arrow points left → native LTR
    { char: '✉️', native: 'ltr' },  // neutral, default LTR
  ];
  const container = document.getElementById('emojiContainer');
  
  function createEmoji(forceDirection) {
    const emoji = document.createElement('span');
    
    // Pick random emoji
    const pick = emojiConfig[Math.floor(Math.random() * emojiConfig.length)];
    emoji.textContent = pick.char;
    
    // Random direction (or forced)
    const direction = forceDirection || (Math.random() > 0.5 ? 'ltr' : 'rtl');
    
    // Flip if going opposite to native direction
    const needsFlip = direction !== pick.native;
    emoji.className = 'floating-emoji ' + direction + (needsFlip ? ' flipped' : '');
    
    // Random vertical position in header area
    const topPos = Math.random() * 80 + 10; // 10% to 90% of header height
    emoji.style.top = topPos + '%';
    
    // Random duration (fast: 5-8 seconds)
    const duration = 5 + Math.random() * 3;
    emoji.style.animationDuration = duration + 's';
    
    // Slight random size variation
    const size = 1.2 + Math.random() * 0.8;
    emoji.style.fontSize = size + 'rem';
    
    container.appendChild(emoji);
    
    // Remove after animation completes
    setTimeout(() => {
      emoji.remove();
    }, duration * 1000);
  }
  
  // Cycling spawn rate: busy -> quiet -> busy -> quiet...
  let phase = 0; // 0 = busy, 1 = quiet
  const busyDuration = 6000;  // 6 seconds of busy
  const quietDuration = 8000; // 8 seconds of quiet
  
  function getSpawnDelay() {
    if (phase === 0) {
      // Busy: spawn every 0.8-1.2 seconds
      return 800 + Math.random() * 400;
    } else {
      // Quiet: spawn every 3-5 seconds
      return 3000 + Math.random() * 2000;
    }
  }
  
  function scheduleNext() {
    const delay = getSpawnDelay();
    setTimeout(() => {
      createEmoji();
      scheduleNext();
    }, delay);
  }
  
  // Cycle between busy and quiet phases
  function cyclePhase() {
    phase = (phase + 1) % 2;
    const duration = phase === 0 ? busyDuration : quietDuration;
    setTimeout(cyclePhase, duration);
  }
  
  // Start with a few emojis
  for (let i = 0; i < 4; i++) {
    setTimeout(() => createEmoji(), i * 300);
  }
  scheduleNext();
  
  // Start phase cycling after initial burst
  setTimeout(cyclePhase, busyDuration);
})();
