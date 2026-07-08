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
          backgroundColor: '#ffffff',
          border: isOpen ? '1px solid #3b82f6' : '1px solid #d1d5db',
          boxShadow: isOpen ? '0 0 0 3px rgba(59, 130, 246, 0.15)' : 'none',
          borderRadius: '8px',
          color: '#1f2937',
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
          color: '#6b7280',
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
            backgroundColor: '#ffffff',
            border: '1px solid #d1d5db',
            borderRadius: '8px',
            boxShadow: '0 10px 15px -3px rgba(0, 0, 0, 0.1), 0 4px 6px -2px rgba(0, 0, 0, 0.05)',
            zIndex: 1000,
            maxHeight: '250px',
            overflowY: 'auto',
            padding: '4px 0'
          }}
        >
          {options.map((option) => (
            <div
              key={option.value}
              onClick={() => {
                onChange(option.value);
                setIsOpen(false);
              }}
              style={{
                padding: '8px 12px',
                fontSize: '0.8rem',
                color: option.value === value ? '#3b82f6' : '#374151',
                backgroundColor: option.value === value ? '#eff6ff' : 'transparent',
                cursor: 'pointer',
                transition: 'background-color 0.15s',
                fontWeight: option.value === value ? 600 : 400
              }}
              onMouseEnter={(e) => {
                if (option.value !== value) {
                  e.currentTarget.style.backgroundColor = '#f3f4f6';
                }
              }}
              onMouseLeave={(e) => {
                if (option.value !== value) {
                  e.currentTarget.style.backgroundColor = 'transparent';
                }
              }}
            >
              {option.label}
            </div>
          ))}
        </div>
      )}
    </div>
  );
}
