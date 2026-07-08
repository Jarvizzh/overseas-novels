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
      style={{ 
        width: `${size}px`, 
        height: `${size}px`, 
        display: 'inline-block',
        verticalAlign: 'middle',
        filter: 'drop-shadow(0 2px 4px rgba(234, 179, 8, 0.2))',
        ...style 
      }}
    >
      {/* Outer ring */}
      <circle cx="12" cy="12" r="10" fill="url(#goldGradient)" stroke="#ca8a04" strokeWidth="1.2" />
      {/* Inner dotted border */}
      <circle cx="12" cy="12" r="7.5" fill="none" stroke="#fef08a" strokeWidth="0.8" strokeDasharray="1,1.5" />
      {/* Center currency symbol */}
      <text 
        x="12" 
        y="15.5" 
        fill="#854d0e" 
        fontSize="11" 
        fontWeight="bold" 
        fontFamily="system-ui, sans-serif"
        textAnchor="middle"
      >
        $
      </text>

      <defs>
        <linearGradient id="goldGradient" x1="0%" y1="0%" x2="100%" y2="100%">
          <stop offset="0%" stopColor="#fef08a" />
          <stop offset="50%" stopColor="#eab308" />
          <stop offset="100%" stopColor="#ca8a04" />
        </linearGradient>
      </defs>
    </svg>
  );
};
