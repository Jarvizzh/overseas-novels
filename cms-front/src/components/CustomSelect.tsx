import React from 'react';

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
  className?: string;
  disabled?: boolean;
  id?: string;
  name?: string;
  'aria-label'?: string;
  'aria-invalid'?: boolean;
  'aria-describedby'?: string;
  'data-policy-field'?: string;
}

export default function CustomSelect({
  options,
  value,
  onChange,
  placeholder,
  width = '180px',
  style,
  className = '',
  disabled = false,
  id,
  name,
  'aria-label': ariaLabel,
  'aria-invalid': invalid,
  'aria-describedby': describedBy,
  'data-policy-field': policyField,
}: CustomSelectProps) {
  const hasEmptyOption = options.some((o) => o.value === '');

  return (
    <select
      id={id}
      name={name}
      aria-label={ariaLabel || placeholder}
      aria-invalid={invalid}
      aria-describedby={describedBy}
      data-policy-field={policyField}
      disabled={disabled}
      value={value}
      onChange={(e) => onChange(e.target.value)}
      className={`cms-select ${className}`}
      style={{ width, ...style }}
    >
      {placeholder && !hasEmptyOption && (
        <option value="">{placeholder}</option>
      )}
      {options.map((option) => (
        <option key={option.value} value={option.value}>
          {option.label}
        </option>
      ))}
    </select>
  );
}
