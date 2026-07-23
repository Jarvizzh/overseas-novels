import { useState, useEffect } from 'react';

interface Option {
  value: string;
  label: string;
}

interface CustomSelectProps {
  options: Option[];
  value: string;
  onChange: (val: string) => void;
  placeholder?: string;
  width?: string;
  style?: React.CSSProperties;
}

export default function CustomSelect({ options, value, onChange, placeholder, width = '180px', style }: CustomSelectProps) {
  const [isOpen, setIsOpen] = useState(false);
  const selectedOption = options.find(o => o.value === value);

  useEffect(() => {
    if (!isOpen) return;
    const handleOutsideClick = () => setIsOpen(false);
    document.addEventListener('click', handleOutsideClick);
    return () => document.removeEventListener('click', handleOutsideClick);
  }, [isOpen]);

  return (
    <div style={{ position: 'relative', width, ...style }} onClick={(e) => e.stopPropagation()}>
      <div 
        className="custom-select-trigger"
        onClick={() => setIsOpen(!isOpen)}
        style={{
          height: '38px',
          backgroundColor: 'hsl(var(--bg-card))',
          border: isOpen ? '1px solid hsl(var(--primary))' : '1px solid hsl(var(--border))',
          boxShadow: isOpen ? '0 0 0 3px hsl(var(--primary) / 0.2)' : 'var(--shadow-sm)',
          borderRadius: '8px',
          color: 'hsl(var(--text-primary))',
          fontSize: '0.8rem',
          padding: '8px 36px 8px 12px',
          cursor: 'pointer',
          display: 'flex',
          alignItems: 'center',
          justifyContent: 'space-between',
          userSelect: 'none',
          position: 'relative',
          transition: 'all 0.15s ease-in-out',
        }}
      >
        <span style={{ overflow: 'hidden', textOverflow: 'ellipsis', whiteSpace: 'nowrap' }}>
          {selectedOption ? selectedOption.label : placeholder}
        </span>
        <span style={{
          position: 'absolute',
          right: '12px',
          top: '50%',
          transform: 'translateY(-50%)',
          display: 'flex',
          alignItems: 'center',
          color: 'hsl(var(--text-secondary))',
          pointerEvents: 'none'
        }}>
          <svg xmlns="http://www.w3.org/2000/svg" fill="none" viewBox="0 0 24 24" strokeWidth="2" stroke="currentColor" style={{ width: '1rem', height: '1rem' }}>
            <path strokeLinecap="round" strokeLinejoin="round" d="M19.5 8.25l-7.5 7.5-7.5-7.5" />
          </svg>
        </span>
      </div>

      {isOpen && (
        <div 
          style={{
            position: 'absolute',
            top: '44px',
            left: 0,
            right: 0,
            backgroundColor: 'hsl(var(--bg-surface))',
            border: '1px solid hsl(var(--border))',
            borderRadius: '8px',
            boxShadow: 'var(--shadow-lg)',
            zIndex: 1000,
            maxHeight: '250px',
            overflowY: 'auto',
            padding: '4px 0'
          }}
        >
          {options.map((option) => {
            const isSelected = option.value === value;
            return (
              <div
                key={option.value}
                onClick={() => {
                  onChange(option.value);
                  setIsOpen(false);
                }}
                style={{
                  padding: '8px 12px',
                  fontSize: '0.8rem',
                  color: isSelected ? 'hsl(var(--primary))' : 'hsl(var(--text-primary))',
                  backgroundColor: isSelected ? 'hsl(var(--primary) / 0.15)' : 'transparent',
                  cursor: 'pointer',
                  transition: 'background-color 0.15s',
                  fontWeight: isSelected ? 600 : 400
                }}
                onMouseEnter={(e) => {
                  if (!isSelected) {
                    e.currentTarget.style.backgroundColor = 'hsl(var(--bg-base))';
                  }
                }}
                onMouseLeave={(e) => {
                  if (!isSelected) {
                    e.currentTarget.style.backgroundColor = 'transparent';
                  }
                }}
              >
                {option.label}
              </div>
            );
          })}
        </div>
      )}
    </div>
  );
}
