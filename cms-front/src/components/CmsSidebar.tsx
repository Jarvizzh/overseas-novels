import React, { useEffect, useId, useRef, useState } from 'react';
import { Link, useLocation } from 'react-router-dom';
import { ChevronDown, ChevronLeft, ChevronRight, X } from 'lucide-react';
import type { ResolvedTheme } from '../utils/theme';
import { isNavPathActive, type NavNode } from '../config/navigation';

interface CmsSidebarProps {
  navTree: NavNode[];
  resolvedTheme: ResolvedTheme;
  collapsed: boolean;
  onToggleCollapse: () => void;
  expandedGroups: Record<string, boolean>;
  onToggleGroup: (groupId: string) => void;
  mobileOpen: boolean;
  onCloseMobile: () => void;
}

interface RailGroupPopoverProps {
  node: NavNode;
  pathname: string;
}

function RailGroupPopover({ node, pathname }: RailGroupPopoverProps) {
  const uid = useId().replace(/:/g, '');
  const popoverId = `rail-pop-${uid}`;
  const anchorName = `--rail-anchor-${uid}`;
  const wrapperRef = useRef<HTMLDivElement>(null);
  const triggerRef = useRef<HTMLButtonElement>(null);
  const popRef = useRef<HTMLDivElement>(null);
  const closeTimerRef = useRef<number | null>(null);
  const hoverPreviewRef = useRef(false);
  const [popoverOpen, setPopoverOpen] = useState(false);

  const children = node.children || [];
  const hasActiveChild = children.some((child) => isNavPathActive(pathname, child.path));

  const clearCloseTimer = () => {
    if (closeTimerRef.current !== null) {
      window.clearTimeout(closeTimerRef.current);
      closeTimerRef.current = null;
    }
  };

  useEffect(() => () => clearCloseTimer(), []);

  const hidePopover = () => {
    clearCloseTimer();
    const pop = popRef.current;
    if (!pop) return;
    try {
      if (pop.matches(':popover-open')) {
        pop.hidePopover();
      }
    } catch {
      // Ignore on engines without :popover-open selector support
      pop.hidePopover();
    }
    hoverPreviewRef.current = false;
  };

  const handlePointerEnter = (e: React.PointerEvent) => {
    if (e.pointerType === 'touch') return;
    if (typeof window !== 'undefined' && !window.matchMedia('(hover: hover)').matches) return;
    clearCloseTimer();
    const pop = popRef.current;
    const trigger = triggerRef.current;
    if (!pop || !trigger) return;
    try {
      if (!pop.matches(':popover-open')) {
        try {
          (pop as HTMLElement & { showPopover: (opts?: { source?: HTMLElement }) => void }).showPopover({
            source: trigger,
          });
        } catch {
          pop.showPopover();
        }
        hoverPreviewRef.current = true;
      }
    } catch {
      pop.showPopover();
      hoverPreviewRef.current = true;
    }
  };

  const handlePointerLeave = (e: React.PointerEvent) => {
    if (e.pointerType === 'touch') return;
    clearCloseTimer();
    closeTimerRef.current = window.setTimeout(() => {
      if (hoverPreviewRef.current) {
        hidePopover();
      }
    }, 140);
  };

  const handleToggle = (e: React.ToggleEvent<HTMLDivElement>) => {
    const isOpen = e.newState === 'open';
    setPopoverOpen(isOpen);
    if (!isOpen) {
      hoverPreviewRef.current = false;
      return;
    }

    const pop = popRef.current;
    const trigger = triggerRef.current;
    if (!pop || !trigger) return;
    if (typeof CSS !== 'undefined' && CSS.supports('position-area', 'block-end')) return;

    const tr = trigger.getBoundingClientRect();
    const pr = pop.getBoundingClientRect();
    const left = Math.max(12, Math.min(tr.right + 8, window.innerWidth - pr.width - 12));
    const top = Math.max(12, Math.min(tr.top, window.innerHeight - pr.height - 12));
    pop.style.inset = 'auto';
    pop.style.margin = '0';
    pop.style.left = `${left}px`;
    pop.style.top = `${top}px`;
  };

  const handleTriggerClick = (e: React.MouseEvent<HTMLButtonElement>) => {
    if (hoverPreviewRef.current && popoverOpen) {
      // Pin a hover-previewed popover open instead of toggling it closed on click
      e.preventDefault();
      hoverPreviewRef.current = false;
    }
  };

  const handleBlur = (e: React.FocusEvent<HTMLDivElement>) => {
    const next = e.relatedTarget as Node | null;
    if (next && !wrapperRef.current?.contains(next)) {
      hidePopover();
    }
  };

  return (
    <div
      ref={wrapperRef}
      className="relative flex justify-center"
      onPointerEnter={handlePointerEnter}
      onPointerLeave={handlePointerLeave}
      onBlur={handleBlur}
    >
      <button
        ref={triggerRef}
        type="button"
        popoverTarget={popoverId}
        aria-label={node.label}
        aria-expanded={popoverOpen}
        aria-controls={popoverId}
        onClick={handleTriggerClick}
        style={{ anchorName } as React.CSSProperties}
        className={`focus-ring w-9 h-9 rounded-lg inline-flex items-center justify-center cursor-pointer transition-colors ${
          hasActiveChild
            ? 'nav-active-item font-semibold'
            : 'text-[var(--cms-muted-fg)] hover:text-[var(--cms-fg)] hover:bg-[var(--cms-secondary)]'
        }`}
      >
        {node.icon}
      </button>

      <div
        ref={popRef}
        id={popoverId}
        popover="auto"
        onToggle={handleToggle}
        style={{ positionAnchor: anchorName } as React.CSSProperties}
        className="cms-popover anchored-rail-popover w-40 p-1.5 text-left"
      >
        <div className="px-2.5 py-1.5 text-[11.5px] font-semibold text-[var(--cms-muted-fg)] border-b border-[var(--cms-border)] mb-1">
          {node.label}
        </div>
        <ul className="space-y-0.5 list-none m-0 p-0">
          {children.map((child) => {
            const active = isNavPathActive(pathname, child.path);
            return (
              <li key={child.id}>
                <Link
                  to={child.path}
                  aria-current={active ? 'page' : undefined}
                  onClick={() => hidePopover()}
                  className={`focus-ring flex items-center justify-between px-2.5 py-1.5 rounded-lg text-[13.5px] no-underline transition-colors ${
                    active
                      ? 'nav-active-item font-semibold'
                      : 'text-[var(--cms-muted-fg)] hover:text-[var(--cms-fg)] hover:bg-[var(--cms-secondary)] font-medium'
                  }`}
                >
                  <span className="truncate">{child.label}</span>
                  {active && (
                    <span
                      className="w-1.5 h-1.5 rounded-full bg-[var(--cms-primary)] shrink-0"
                      aria-hidden="true"
                    />
                  )}
                </Link>
              </li>
            );
          })}
        </ul>
      </div>
    </div>
  );
}

interface ExpandedNavListProps {
  navTree: NavNode[];
  pathname: string;
  expandedGroups: Record<string, boolean>;
  onToggleGroup: (groupId: string) => void;
  onNavigate?: () => void;
  idPrefix: string;
}

function ExpandedNavList({
  navTree,
  pathname,
  expandedGroups,
  onToggleGroup,
  onNavigate,
  idPrefix,
}: ExpandedNavListProps) {
  return (
    <ul className="space-y-1 list-none m-0 p-0">
      {navTree.map((node) => {
        if (!node.children && node.path) {
          const active = isNavPathActive(pathname, node.path);
          return (
            <li key={node.id}>
              <Link
                to={node.path}
                aria-current={active ? 'page' : undefined}
                onClick={onNavigate}
                className={`focus-ring relative w-full flex items-center gap-2 px-2.5 py-1.5 rounded-lg text-[13.5px] no-underline transition-colors ${
                  active
                    ? 'nav-active-item font-semibold'
                    : 'text-[var(--cms-muted-fg)] hover:text-[var(--cms-fg)] hover:bg-[var(--cms-secondary)] font-medium'
                }`}
              >
                <span className="shrink-0 inline-flex items-center">{node.icon}</span>
                <span className="truncate">{node.label}</span>
              </Link>
            </li>
          );
        }

        const children = node.children || [];
        const isOpen = expandedGroups[node.id] ?? true;
        const hasActiveChild = children.some((child) => isNavPathActive(pathname, child.path));
        const submenuId = `${idPrefix}-group-${node.id}`;

        return (
          <li key={node.id}>
            <button
              type="button"
              aria-expanded={isOpen}
              aria-controls={submenuId}
              onClick={() => onToggleGroup(node.id)}
              className={`focus-ring w-full flex items-center justify-between gap-1.5 px-2.5 py-1.5 rounded-lg text-[13.5px] font-medium cursor-pointer transition-colors ${
                hasActiveChild
                  ? 'text-[var(--cms-fg)] font-semibold'
                  : 'text-[var(--cms-muted-fg)] hover:text-[var(--cms-fg)] hover:bg-[var(--cms-secondary)]'
              }`}
            >
              <span className="flex items-center gap-2 min-w-0">
                <span
                  className={`shrink-0 inline-flex items-center ${
                    hasActiveChild ? 'text-[var(--cms-primary)]' : ''
                  }`}
                >
                  {node.icon}
                </span>
                <span className="truncate">{node.label}</span>
              </span>
              <span className="flex items-center gap-1.5 shrink-0">
                {!isOpen && hasActiveChild && (
                  <span
                    className="w-1.5 h-1.5 rounded-full bg-[var(--cms-primary)]"
                    aria-hidden="true"
                  />
                )}
                <ChevronDown
                  size={13}
                  aria-hidden="true"
                  className={`text-[var(--cms-muted-fg)] transition-transform duration-150 ${
                    isOpen ? 'rotate-0' : '-rotate-90'
                  }`}
                />
              </span>
            </button>

            <ul
              id={submenuId}
              hidden={!isOpen}
              className="ml-[17px] pl-2.5 border-l border-[var(--cms-border)] space-y-0.5 mt-0.5 mb-1 list-none"
            >
              {children.map((child) => {
                const active = isNavPathActive(pathname, child.path);
                return (
                  <li key={child.id}>
                    <Link
                      to={child.path}
                      aria-current={active ? 'page' : undefined}
                      onClick={onNavigate}
                      className={`focus-ring relative w-full flex items-center px-2.5 py-1.5 rounded-lg text-[13.5px] no-underline transition-colors ${
                        active
                          ? "nav-active-item font-semibold before:content-[''] before:absolute before:-left-[11px] before:top-1.5 before:bottom-1.5 before:w-[2px] before:rounded-full before:bg-[var(--cms-primary)]"
                          : 'text-[var(--cms-muted-fg)] hover:text-[var(--cms-fg)] hover:bg-[var(--cms-secondary)] font-normal'
                      }`}
                    >
                      <span className="truncate">{child.label}</span>
                    </Link>
                  </li>
                );
              })}
            </ul>
          </li>
        );
      })}
    </ul>
  );
}

export function CmsSidebar({
  navTree,
  resolvedTheme,
  collapsed,
  onToggleCollapse,
  expandedGroups,
  onToggleGroup,
  mobileOpen,
  onCloseMobile,
}: CmsSidebarProps) {
  const { pathname } = useLocation();
  const mobileDialogRef = useRef<HTMLDialogElement>(null);
  const mobileCloseBtnRef = useRef<HTMLButtonElement>(null);
  const pressedOutsideRef = useRef(false);
  const logoSrc =
    resolvedTheme === 'dark' ? '/assets/logo_dark.png?v=twin_exact' : '/assets/logo_light.png?v=twin_exact';

  useEffect(() => {
    const el = mobileDialogRef.current;
    if (!el) return;
    if (mobileOpen) {
      if (!el.open) {
        el.showModal();
        mobileCloseBtnRef.current?.focus();
      }
    } else if (el.open) {
      el.close();
    }
  }, [mobileOpen]);

  // Close the mobile top-layer dialog when viewport expands to desktop (>= 768px)
  useEffect(() => {
    if (!mobileOpen) return;
    const mq = window.matchMedia('(min-width: 768px)');
    const syncDesktopViewport = () => {
      if (mq.matches) {
        onCloseMobile();
      }
    };
    syncDesktopViewport();
    mq.addEventListener('change', syncDesktopViewport);
    return () => mq.removeEventListener('change', syncDesktopViewport);
  }, [mobileOpen, onCloseMobile]);

  // Light-dismiss fallback when closedby is unsupported
  useEffect(() => {
    const el = mobileDialogRef.current;
    if (!el || 'closedBy' in HTMLDialogElement.prototype) return;

    const isOutside = (e: PointerEvent) => {
      if (e.target !== el) return false;
      const r = el.getBoundingClientRect();
      return e.clientX < r.left || e.clientX > r.right || e.clientY < r.top || e.clientY > r.bottom;
    };
    const onPointerDown = (e: PointerEvent) => {
      pressedOutsideRef.current = isOutside(e);
    };
    const onPointerUp = (e: PointerEvent) => {
      if (pressedOutsideRef.current && isOutside(e)) {
        el.close();
      }
      pressedOutsideRef.current = false;
    };

    el.addEventListener('pointerdown', onPointerDown);
    el.addEventListener('pointerup', onPointerUp);
    return () => {
      el.removeEventListener('pointerdown', onPointerDown);
      el.removeEventListener('pointerup', onPointerUp);
    };
  }, []);

  return (
    <>
      {/* Desktop Narrow Collapsible Sidebar (184px expanded / 52px collapsed) */}
      <aside
        className={`hidden md:flex shrink-0 bg-[var(--cms-sidebar)] border-r border-[var(--cms-border)] sticky top-0 h-screen flex-col select-none transition-[width] duration-200 z-20 ${
          collapsed ? 'w-[52px]' : 'w-[184px]'
        }`}
      >
        {/* Brand Bar */}
        <div
          className={`h-12 border-b border-[var(--cms-border)] flex items-center shrink-0 ${
            collapsed ? 'justify-center px-1.5' : 'justify-between gap-1.5 px-2.5'
          }`}
        >
          <Link
            to="/"
            title={collapsed ? 'STAR NOVEL · 首页总览' : undefined}
            className="flex items-center gap-2 min-w-0 no-underline"
          >
            <img
              src={logoSrc}
              alt="STAR NOVEL"
              className="w-7 h-7 rounded-lg object-contain border border-[var(--cms-border)] shrink-0"
            />
            {!collapsed && (
              <span className="text-[13.5px] font-semibold tracking-tight text-[var(--cms-fg)] truncate">
                STAR NOVEL
              </span>
            )}
          </Link>

          {!collapsed && (
            <button
              type="button"
              onClick={onToggleCollapse}
              aria-label="收起导航栏"
              title="收起导航栏"
              className="focus-ring w-6 h-6 rounded-md text-[var(--cms-muted-fg)] hover:text-[var(--cms-fg)] hover:bg-[var(--cms-secondary)] inline-flex items-center justify-center cursor-pointer transition-colors shrink-0"
            >
              <ChevronLeft size={14} aria-hidden="true" />
            </button>
          )}
        </div>

        {/* Dedicated Expand Control in Collapsed Rail */}
        {collapsed && (
          <div className="py-1.5 border-b border-[var(--cms-border)] flex justify-center shrink-0">
            <button
              type="button"
              onClick={onToggleCollapse}
              aria-label="展开导航栏"
              title="展开导航栏"
              className="focus-ring w-9 h-7 rounded-md text-[var(--cms-muted-fg)] hover:text-[var(--cms-fg)] hover:bg-[var(--cms-secondary)] inline-flex items-center justify-center cursor-pointer transition-colors"
            >
              <ChevronRight size={14} aria-hidden="true" />
            </button>
          </div>
        )}

        {/* Navigation */}
        <nav
          className={`cms-nav-scroll flex-1 overflow-y-auto ${collapsed ? 'px-1.5 py-2 space-y-1.5' : 'px-2 py-2.5'}`}
          aria-label="主导航"
        >
          {collapsed ? (
            <div className="space-y-1.5">
              {navTree.map((node) => {
                if (!node.children && node.path) {
                  const active = isNavPathActive(pathname, node.path);
                  return (
                    <div key={node.id} className="flex justify-center">
                      <Link
                        to={node.path}
                        aria-label={node.label}
                        title={node.label}
                        aria-current={active ? 'page' : undefined}
                        className={`focus-ring w-9 h-9 rounded-lg inline-flex items-center justify-center no-underline transition-colors ${
                          active
                            ? 'nav-active-item font-semibold'
                            : 'text-[var(--cms-muted-fg)] hover:text-[var(--cms-fg)] hover:bg-[var(--cms-secondary)]'
                        }`}
                      >
                        {node.icon}
                      </Link>
                    </div>
                  );
                }

                return <RailGroupPopover key={node.id} node={node} pathname={pathname} />;
              })}
            </div>
          ) : (
            <ExpandedNavList
              navTree={navTree}
              pathname={pathname}
              expandedGroups={expandedGroups}
              onToggleGroup={onToggleGroup}
              idPrefix="desktop"
            />
          )}
        </nav>
      </aside>

      {/* Mobile Navigation Drawer (< md) */}
      <dialog
        ref={mobileDialogRef}
        id="mobile-nav-drawer"
        aria-label="主导航"
        {...({ closedby: 'any' } as Record<string, string>)}
        onClose={() => {
          if (mobileOpen) {
            onCloseMobile();
          }
        }}
        className="cms-nav-drawer md:hidden"
      >
        {mobileOpen && (
          <div className="flex flex-col h-full">
            <div className="h-12 px-3 border-b border-[var(--cms-border)] flex items-center justify-between gap-2 shrink-0">
              <Link to="/" onClick={onCloseMobile} className="flex items-center gap-2 min-w-0 no-underline">
                <img
                  src={logoSrc}
                  alt="STAR NOVEL"
                  className="w-7 h-7 rounded-lg object-contain border border-[var(--cms-border)] shrink-0"
                />
                <span className="text-[13.5px] font-semibold tracking-tight text-[var(--cms-fg)] truncate">
                  STAR NOVEL
                </span>
              </Link>
              <button
                ref={mobileCloseBtnRef}
                type="button"
                aria-label="关闭导航菜单"
                onClick={onCloseMobile}
                className="focus-ring w-7 h-7 rounded-md text-[var(--cms-muted-fg)] hover:text-[var(--cms-fg)] hover:bg-[var(--cms-secondary)] inline-flex items-center justify-center cursor-pointer transition-colors"
              >
                <X size={16} aria-hidden="true" />
              </button>
            </div>

            <nav className="cms-nav-scroll flex-1 overflow-y-auto px-2.5 py-2.5" aria-label="移动端导航">
              <ExpandedNavList
                navTree={navTree}
                pathname={pathname}
                expandedGroups={expandedGroups}
                onToggleGroup={onToggleGroup}
                onNavigate={onCloseMobile}
                idPrefix="mobile"
              />
            </nav>
          </div>
        )}
      </dialog>
    </>
  );
}
