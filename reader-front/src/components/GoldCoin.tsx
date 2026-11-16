import React from 'react';

interface GoldCoinProps {
  size?: number;
  style?: React.CSSProperties;
}

export const GoldCoin: React.FC<GoldCoinProps> = ({ size = 18, style }) => {
  return (
    <svg
      xmlns="http://www.w3.org/2000/svg"
      viewBox="0 0 24 24"
      aria-hidden="true"
      style={{
        width: `${size}px`,
        height: `${size}px`,
        display: 'inline-block',
        verticalAlign: 'middle',
        flexShrink: 0,
        ...style,
      }}
    >
      <circle cx="12" cy="12" r="10" fill="url(#snCoinGrad)" stroke="#9A7132" strokeWidth="1.25" />
      <circle cx="12" cy="12" r="7.5" fill="none" stroke="rgba(255, 248, 225, 0.55)" strokeWidth="0.75" />
      <path
        d="M12 6.5l1.35 3.65L17 11.5l-3.65 1.35L12 16.5l-1.35-3.65L7 11.5l3.65-1.35L12 6.5z"
        fill="#6B4A16"
      />
      <defs>
        <linearGradient id="snCoinGrad" x1="0%" y1="0%" x2="100%" y2="100%">
          <stop offset="0%" stopColor="#F5E1A4" />
          <stop offset="50%" stopColor="#D4AE68" />
          <stop offset="100%" stopColor="#A67C37" />
        </linearGradient>
      </defs>
    </svg>
  );
};
