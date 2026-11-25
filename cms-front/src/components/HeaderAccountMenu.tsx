import { ChevronDown, LogOut } from 'lucide-react';
import type { AdminUser } from '../utils/api';
import { ROLE_LABELS } from '../config/navigation';
import { ToolbarPopover } from './ui/Primitives';

interface HeaderAccountMenuProps {
  admin: AdminUser;
  onLogout: () => void;
}

export function HeaderAccountMenu({ admin, onLogout }: HeaderAccountMenuProps) {
  const displayName = admin.nickname || admin.username;
  const roleLabel = ROLE_LABELS[admin.role] || admin.role;
  const initial = (displayName || 'A').charAt(0).toUpperCase();

  return (
    <ToolbarPopover
      align="end"
      ariaLabel={`账号菜单：${displayName}`}
      panelClassName="w-52 p-1.5 space-y-0.5"
      triggerClassName="h-[34px] pl-1.5 pr-2 rounded-lg border border-[var(--cms-border)] bg-[var(--cms-card)] hover:bg-[var(--cms-secondary)] text-[var(--cms-fg)] transition-colors"
      label={
        <span className="inline-flex items-center gap-2 min-w-0">
          <span className="w-6 h-6 rounded-full nav-active-item text-[11px] font-semibold inline-flex items-center justify-center shrink-0">
            {initial}
          </span>
          <span className="text-xs font-medium text-[var(--cms-fg)] truncate max-w-[96px] sm:max-w-[120px]">
            {displayName}
          </span>
          <ChevronDown size={13} className="text-[var(--cms-muted-fg)] shrink-0" aria-hidden="true" />
        </span>
      }
    >
      {({ close }) => (
        <>
          <div className="px-3 py-2 text-xs border-b border-[var(--cms-border)] mb-1">
            <div className="font-medium text-[var(--cms-fg)] truncate">{displayName}</div>
            {admin.username && admin.username !== displayName && (
              <div className="text-[11px] text-[var(--cms-muted-fg)] truncate mt-0.5">
                账号：{admin.username}
              </div>
            )}
            <div className="text-[var(--cms-muted-fg)] mt-0.5">角色：{roleLabel}</div>
          </div>
          <button
            type="button"
            onClick={() => {
              close();
              onLogout();
            }}
            className="focus-ring w-full px-3 py-2 rounded-lg text-xs font-medium text-[var(--cms-error)] hover:bg-[color-mix(in_srgb,var(--cms-error)_10%,transparent)] flex items-center gap-2 cursor-pointer transition-colors"
          >
            <LogOut size={14} aria-hidden="true" />
            <span>退出登录</span>
          </button>
        </>
      )}
    </ToolbarPopover>
  );
}
