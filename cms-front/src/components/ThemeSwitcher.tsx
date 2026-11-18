import { Monitor, Moon, Sun } from 'lucide-react';
import type { ResolvedTheme, ThemeMode } from '../utils/theme';

interface ThemeSwitcherProps {
  mode: ThemeMode;
  resolvedTheme: ResolvedTheme;
  onChange: (mode: ThemeMode) => void;
}

export function ThemeSwitcher({ mode, resolvedTheme, onChange }: ThemeSwitcherProps) {
  const options: Array<{
    value: ThemeMode;
    label: string;
    title: string;
    icon: React.ReactNode;
  }> = [
    {
      value: 'system',
      label: `跟随系统（当前${resolvedTheme === 'dark' ? '深邃黑' : '简约白蓝'}）`,
      title: `跟随系统（当前${resolvedTheme === 'dark' ? '深色' : '浅色'}）`,
      icon: (
        <span className="relative inline-flex items-center justify-center">
          <Monitor size={14} aria-hidden="true" />
          <span
            aria-hidden="true"
            className="absolute -bottom-0.5 -right-0.5 w-1.5 h-1.5 rounded-full border border-[var(--cms-card)]"
            style={{
              backgroundColor: resolvedTheme === 'dark' ? '#60a5fa' : '#f59e0b',
            }}
          />
        </span>
      ),
    },
    {
      value: 'light',
      label: '简约白蓝（浅色）',
      title: '简约白蓝',
      icon: <Sun size={14} aria-hidden="true" />,
    },
    {
      value: 'dark',
      label: '深邃黑（深色）',
      title: '深邃黑',
      icon: <Moon size={14} aria-hidden="true" />,
    },
  ];

  return (
    <div
      role="group"
      aria-label="主题切换"
      className="inline-flex items-center rounded-lg border border-[var(--cms-border)] bg-[var(--cms-card)] p-0.5 gap-0.5"
    >
      {options.map((opt) => {
        const active = mode === opt.value;
        return (
          <button
            key={opt.value}
            type="button"
            aria-label={opt.label}
            aria-pressed={active}
            title={opt.title}
            onClick={() => onChange(opt.value)}
            className={`focus-ring w-7 h-7 rounded-md inline-flex items-center justify-center cursor-pointer transition-colors ${
              active
                ? 'bg-[var(--cms-secondary)] text-[var(--cms-fg)] shadow-xs'
                : 'text-[var(--cms-muted-fg)] hover:text-[var(--cms-fg)]'
            }`}
          >
            {opt.icon}
          </button>
        );
      })}
    </div>
  );
}
