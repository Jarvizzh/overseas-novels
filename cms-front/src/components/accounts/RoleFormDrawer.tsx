import { useEffect, useMemo, useState } from 'react';
import { ChevronDown, ChevronRight, Lock, ShieldCheck } from 'lucide-react';
import {
  authApi,
  type PermissionGroupMeta,
  type PermissionModuleMeta,
  type RoleItem,
} from '../../utils/api';
import CustomSelect from '../CustomSelect';
import { CmsDialog } from '../ui/CmsDialog';

interface RoleFormDrawerProps {
  open: boolean;
  mode: 'create' | 'edit' | 'view';
  role: RoleItem | null;
  catalog: PermissionGroupMeta[];
  catalogLoading: boolean;
  onClose: () => void;
  onSaved: () => void;
}

function getModuleActions(mod: PermissionModuleMeta) {
  const readAction = mod.read ?? mod.actions?.find((a) => a.action === 'read');
  const writeAction = mod.write ?? mod.actions?.find((a) => a.action === 'write');
  const otherActions = (mod.actions || []).filter(
    (a) => a.code !== readAction?.code && a.code !== writeAction?.code,
  );
  return { readAction, writeAction, otherActions };
}

export function RoleFormDrawer({
  open,
  mode,
  role,
  catalog,
  catalogLoading,
  onClose,
  onSaved,
}: RoleFormDrawerProps) {
  const isSuperAdminRole = Boolean(role?.is_super_admin);
  const readOnly = mode === 'view' || isSuperAdminRole;

  const [code, setCode] = useState('');
  const [name, setName] = useState('');
  const [description, setDescription] = useState('');
  const [status, setStatus] = useState<number>(1);
  const [selectedPerms, setSelectedPerms] = useState<Set<string>>(new Set());
  const [expandedGroups, setExpandedGroups] = useState<Record<string, boolean>>({});
  const [submitting, setSubmitting] = useState(false);
  const [formError, setFormError] = useState('');

  const superAdminOnlyCodes = useMemo(() => {
    const set = new Set<string>();
    for (const group of catalog) {
      for (const mod of group.modules) {
        const { readAction, writeAction, otherActions } = getModuleActions(mod);
        for (const act of [readAction, writeAction, ...otherActions]) {
          if (act?.super_admin_only) set.add(act.code);
        }
      }
    }
    return set;
  }, [catalog]);

  useEffect(() => {
    if (!open) return;
    setFormError('');
    if (role && mode !== 'create') {
      setCode(role.code);
      setName(role.name);
      setDescription(role.description || '');
      setStatus(role.status ?? 1);
      setSelectedPerms(new Set(role.permissions || []));
    } else {
      setCode('');
      setName('');
      setDescription('');
      setStatus(1);
      setSelectedPerms(new Set(['dashboard:read']));
    }
    setExpandedGroups(Object.fromEntries(catalog.map((g) => [g.group, true])));
  }, [open, mode, role, catalog]);

  const toggleGroupExpand = (groupKey: string) => {
    setExpandedGroups((prev) => ({ ...prev, [groupKey]: !(prev[groupKey] ?? true) }));
  };

  const handleToggleRead = (mod: PermissionModuleMeta, checked: boolean) => {
    if (readOnly) return;
    const { readAction, writeAction } = getModuleActions(mod);
    if (!readAction || readAction.super_admin_only) return;
    setSelectedPerms((prev) => {
      const next = new Set(prev);
      if (checked) {
        next.add(readAction.code);
      } else {
        next.delete(readAction.code);
        if (writeAction) {
          next.delete(writeAction.code);
        }
      }
      return next;
    });
  };

  const handleToggleWrite = (mod: PermissionModuleMeta, checked: boolean) => {
    if (readOnly) return;
    const { readAction, writeAction } = getModuleActions(mod);
    if (!writeAction || writeAction.super_admin_only) return;
    setSelectedPerms((prev) => {
      const next = new Set(prev);
      if (checked) {
        next.add(writeAction.code);
        if (readAction && !readAction.super_admin_only) {
          next.add(readAction.code);
        }
      } else {
        next.delete(writeAction.code);
      }
      return next;
    });
  };

  const handleToggleOther = (permCode: string, isSuperOnly: boolean, checked: boolean) => {
    if (readOnly || isSuperOnly) return;
    setSelectedPerms((prev) => {
      const next = new Set(prev);
      if (checked) next.add(permCode);
      else next.delete(permCode);
      return next;
    });
  };

  const applyGroupPreset = (group: PermissionGroupMeta, preset: 'read' | 'readwrite' | 'clear') => {
    if (readOnly) return;
    setSelectedPerms((prev) => {
      const next = new Set(prev);
      for (const mod of group.modules) {
        const { readAction, writeAction, otherActions } = getModuleActions(mod);
        if (preset === 'clear') {
          if (readAction) next.delete(readAction.code);
          if (writeAction) next.delete(writeAction.code);
          for (const a of otherActions) next.delete(a.code);
        } else if (preset === 'read') {
          if (readAction && !readAction.super_admin_only) next.add(readAction.code);
          if (writeAction) next.delete(writeAction.code);
        } else {
          if (readAction && !readAction.super_admin_only) next.add(readAction.code);
          if (writeAction && !writeAction.super_admin_only) next.add(writeAction.code);
          for (const a of otherActions) {
            if (!a.super_admin_only) next.add(a.code);
          }
        }
      }
      return next;
    });
  };

  const handleSubmit = async (e: React.FormEvent) => {
    e.preventDefault();
    if (readOnly || submitting) return;

    const trimmedName = name.trim();
    const trimmedCode = code.trim();
    if (!trimmedName) {
      setFormError('请输入角色名称');
      return;
    }
    if (mode === 'create' && !trimmedCode) {
      setFormError('请输入角色标识 (Code)');
      return;
    }

    const finalPerms = Array.from(selectedPerms).filter((p) => !superAdminOnlyCodes.has(p));

    setSubmitting(true);
    setFormError('');
    try {
      if (mode === 'create') {
        await authApi.createRole({
          code: trimmedCode,
          name: trimmedName,
          description: description.trim(),
          status,
          permissions: finalPerms,
        });
        window.showToast?.(`角色「${trimmedName}」创建成功`, 'success');
      } else if (role) {
        await authApi.updateRole(role.id, {
          name: trimmedName,
          description: description.trim(),
          status,
          permissions: finalPerms,
        });
        window.showToast?.(`角色「${trimmedName}」权限配置已更新`, 'success');
      }
      onSaved();
      onClose();
    } catch (err: unknown) {
      setFormError((err instanceof Error ? err.message : '') || '保存角色失败，请重试');
    } finally {
      setSubmitting(false);
    }
  };

  const title =
    mode === 'create'
      ? '新建自定义角色'
      : isSuperAdminRole
        ? `查看超级管理员角色 · ${role?.name}`
        : readOnly
          ? `查看角色权限 · ${role?.name}`
          : `编辑角色与权限 · ${role?.name}`;

  const totalAssignedCount = isSuperAdminRole
    ? catalog.reduce(
        (acc, g) =>
          acc +
          g.modules.reduce((mAcc, mod) => {
            const { readAction, writeAction, otherActions } = getModuleActions(mod);
            return mAcc + (readAction ? 1 : 0) + (writeAction ? 1 : 0) + otherActions.length;
          }, 0),
        0,
      )
    : selectedPerms.size;

  return (
    <CmsDialog
      open={open}
      onClose={() => !submitting && onClose()}
      variant="drawer"
      size="lg"
      title={title}
      subtitle={
        role && mode !== 'create'
          ? `${role.code}${role.is_system ? ' · 系统内置角色' : ' · 自定义角色'}`
          : '按导航栏分组配置读/写权限，勾选写权限自动包含读权限'
      }
      closedBy={submitting ? 'none' : 'any'}
      closeDisabled={submitting}
      footer={
        readOnly ? (
          <button type="button" className="btn-secondary" onClick={onClose}>
            关闭
          </button>
        ) : (
          <>
            <button type="button" className="btn-secondary" disabled={submitting} onClick={onClose}>
              取消
            </button>
            <button type="submit" form="role-permission-form" disabled={submitting} className="btn-primary">
              {submitting ? '保存中...' : mode === 'create' ? '确认创建' : '保存权限配置'}
            </button>
          </>
        )
      }
    >
      <form id="role-permission-form" onSubmit={handleSubmit} className="space-y-5 text-xs">
        {formError && (
          <div
            role="alert"
            className="p-3 rounded-xl border font-medium"
            style={{
              backgroundColor: 'color-mix(in srgb, var(--cms-error) 10%, transparent)',
              borderColor: 'color-mix(in srgb, var(--cms-error) 25%, transparent)',
              color: 'var(--cms-error)',
            }}
          >
            {formError}
          </div>
        )}

        {isSuperAdminRole && (
          <div className="p-3.5 rounded-xl border border-[var(--cms-primary)]/30 bg-[color-mix(in_srgb,var(--cms-primary)_8%,transparent)] flex items-start gap-2.5 text-[var(--cms-fg)]">
            <ShieldCheck size={16} className="text-[var(--cms-primary)] shrink-0 mt-0.5" aria-hidden="true" />
            <div className="leading-relaxed">
              <div className="font-semibold">超级管理员角色已锁定（全量权限）</div>
              <div className="text-[var(--cms-muted-fg)] mt-0.5">
                内置超级管理员自动拥有全部功能及超管独占写权限，不支持编辑或降权。
              </div>
            </div>
          </div>
        )}

        {/* Basic Info */}
        <div className="grid grid-cols-1 sm:grid-cols-2 gap-4">
          <label className="block space-y-1.5">
            <span className="block font-medium text-[var(--cms-muted-fg)]">
              角色名称 <span className="text-[var(--cms-error)]">*</span>
            </span>
            <input
              type="text"
              className="input-field"
              placeholder="例如：投放主管 / 内容审核"
              value={name}
              onChange={(e) => setName(e.target.value)}
              disabled={readOnly}
              required
            />
          </label>

          <label className="block space-y-1.5">
            <span className="block font-medium text-[var(--cms-muted-fg)]">
              角色标识 (Code) <span className="text-[var(--cms-error)]">*</span>
            </span>
            <input
              type="text"
              className="input-field font-mono-code"
              placeholder="例如：AdLead（字母/数字/下划线）"
              value={code}
              onChange={(e) => setCode(e.target.value)}
              disabled={readOnly || mode !== 'create'}
              required
            />
          </label>
        </div>

        <div className="grid grid-cols-1 sm:grid-cols-[1fr_180px] gap-4">
          <label className="block space-y-1.5">
            <span className="block font-medium text-[var(--cms-muted-fg)]">角色说明</span>
            <input
              type="text"
              className="input-field"
              placeholder="简要描述该岗位的职责范围..."
              value={description}
              onChange={(e) => setDescription(e.target.value)}
              disabled={readOnly}
            />
          </label>

          <label className="block space-y-1.5">
            <span className="block font-medium text-[var(--cms-muted-fg)]">角色状态</span>
            <CustomSelect
              aria-label="角色状态"
              width="100%"
              value={String(status)}
              onChange={(val) => setStatus(Number(val))}
              disabled={readOnly || Boolean(role?.is_super_admin)}
              options={[
                { value: '1', label: '正常启用' },
                { value: '2', label: '已禁用' },
              ]}
            />
          </label>
        </div>

        {/* Permission Matrix */}
        <div className="space-y-3 pt-1">
          <div className="flex flex-wrap items-center justify-between gap-2">
            <div>
              <h3 className="text-sm font-semibold text-[var(--cms-fg)] m-0">导航栏分级权限矩阵</h3>
              <p className="text-[11px] text-[var(--cms-muted-fg)] mt-0.5 m-0">
                勾选写权限会自动勾选同模块读权限；取消读权限会自动取消同模块写权限。
              </p>
            </div>
            <span className="text-xs text-[var(--cms-muted-fg)] tabular-nums">
              已授权 <strong className="text-[var(--cms-fg)]">{totalAssignedCount}</strong> 项
            </span>
          </div>

          {catalogLoading ? (
            <div className="space-y-2.5">
              {[1, 2, 3].map((i) => (
                <div key={i} className="skeleton-bar h-20 rounded-xl" />
              ))}
            </div>
          ) : (
            <div className="space-y-2.5">
              {catalog.map((group) => {
                const isExpanded = expandedGroups[group.group] ?? true;
                let groupTotal = 0;
                let groupChecked = 0;
                for (const mod of group.modules) {
                  const { readAction, writeAction, otherActions } = getModuleActions(mod);
                  for (const act of [readAction, writeAction, ...otherActions]) {
                    if (!act) continue;
                    groupTotal += 1;
                    if (isSuperAdminRole || selectedPerms.has(act.code)) groupChecked += 1;
                  }
                }

                return (
                  <div
                    key={group.group}
                    className="rounded-xl border border-[var(--cms-border)] bg-[var(--cms-card)] overflow-hidden"
                  >
                    {/* Group Header */}
                    <div className="px-3.5 py-2.5 bg-[var(--cms-content)]/70 border-b border-[var(--cms-border)] flex flex-wrap items-center justify-between gap-2">
                      <button
                        type="button"
                        onClick={() => toggleGroupExpand(group.group)}
                        aria-expanded={isExpanded}
                        className="focus-ring inline-flex items-center gap-2 font-semibold text-[var(--cms-fg)] cursor-pointer rounded-md px-1 py-0.5"
                      >
                        {isExpanded ? (
                          <ChevronDown size={14} className="text-[var(--cms-muted-fg)]" aria-hidden="true" />
                        ) : (
                          <ChevronRight size={14} className="text-[var(--cms-muted-fg)]" aria-hidden="true" />
                        )}
                        <span>{group.label}</span>
                        <span className="text-[11px] font-normal text-[var(--cms-muted-fg)] tabular-nums">
                          ({groupChecked}/{groupTotal})
                        </span>
                      </button>

                      {!readOnly && (
                        <div className="flex items-center gap-1.5">
                          <button
                            type="button"
                            onClick={() => applyGroupPreset(group, 'read')}
                            className="btn-ghost h-6 px-2 text-[11px] text-[var(--cms-muted-fg)] hover:text-[var(--cms-fg)]"
                          >
                            全选只读
                          </button>
                          <button
                            type="button"
                            onClick={() => applyGroupPreset(group, 'readwrite')}
                            className="btn-ghost h-6 px-2 text-[11px] text-[var(--cms-primary)]"
                          >
                            全选读写
                          </button>
                          <button
                            type="button"
                            onClick={() => applyGroupPreset(group, 'clear')}
                            className="btn-ghost h-6 px-2 text-[11px] text-[var(--cms-muted-fg)] hover:text-[var(--cms-error)]"
                          >
                            清空
                          </button>
                        </div>
                      )}
                    </div>

                    {/* Group Modules */}
                    {isExpanded && (
                      <div className="divide-y divide-[var(--cms-border)]">
                        {group.modules.map((mod) => {
                          const { readAction, writeAction, otherActions } = getModuleActions(mod);
                          const readChecked = Boolean(
                            readAction && (isSuperAdminRole || selectedPerms.has(readAction.code)),
                          );
                          const writeLockedSuperOnly = Boolean(
                            writeAction?.super_admin_only && !isSuperAdminRole,
                          );
                          const writeChecked = Boolean(
                            writeAction &&
                              (isSuperAdminRole || (!writeLockedSuperOnly && selectedPerms.has(writeAction.code))),
                          );

                          return (
                            <div
                              key={mod.resource}
                              className="px-4 py-2.5 flex flex-wrap items-center justify-between gap-3"
                            >
                              <div className="min-w-[160px] flex-1">
                                <div className="font-medium text-[var(--cms-fg)]">{mod.label}</div>
                                {mod.description && (
                                  <div className="text-[11px] text-[var(--cms-muted-fg)] mt-0.5">
                                    {mod.description}
                                  </div>
                                )}
                              </div>

                              <div className="flex flex-wrap items-center gap-4 shrink-0">
                                {readAction && (
                                  <label
                                    className={`inline-flex items-center gap-1.5 select-none ${
                                      readOnly ? 'cursor-default opacity-80' : 'cursor-pointer'
                                    }`}
                                  >
                                    <input
                                      type="checkbox"
                                      checked={readChecked}
                                      disabled={readOnly || readAction.super_admin_only}
                                      onChange={(e) => handleToggleRead(mod, e.target.checked)}
                                      className="w-3.5 h-3.5 rounded cursor-pointer disabled:cursor-not-allowed"
                                    />
                                    <span className="text-[var(--cms-fg)]">只读</span>
                                  </label>
                                )}

                                {writeAction ? (
                                  <label
                                    title={writeLockedSuperOnly ? '仅超级管理员拥有' : undefined}
                                    className={`inline-flex items-center gap-1.5 select-none ${
                                      readOnly || writeLockedSuperOnly
                                        ? 'cursor-not-allowed opacity-60'
                                        : 'cursor-pointer'
                                    }`}
                                  >
                                    <input
                                      type="checkbox"
                                      checked={writeChecked}
                                      disabled={readOnly || writeLockedSuperOnly}
                                      onChange={(e) => handleToggleWrite(mod, e.target.checked)}
                                      className="w-3.5 h-3.5 rounded cursor-pointer disabled:cursor-not-allowed"
                                    />
                                    <span className="text-[var(--cms-fg)]">读写</span>
                                    {writeLockedSuperOnly && (
                                      <span className="inline-flex items-center gap-1 px-1.5 py-0.5 rounded bg-[var(--cms-secondary)] text-[10px] text-[var(--cms-muted-fg)]">
                                        <Lock size={10} aria-hidden="true" />
                                        仅超级管理员拥有
                                      </span>
                                    )}
                                  </label>
                                ) : (
                                  <span className="text-[11px] text-[var(--cms-muted-fg)] min-w-[44px]">—</span>
                                )}

                                {otherActions.map((act) => {
                                  const locked = act.super_admin_only && !isSuperAdminRole;
                                  const checked = isSuperAdminRole || (!locked && selectedPerms.has(act.code));
                                  return (
                                    <label
                                      key={act.code}
                                      className={`inline-flex items-center gap-1.5 select-none ${
                                        readOnly || locked ? 'cursor-not-allowed opacity-60' : 'cursor-pointer'
                                      }`}
                                    >
                                      <input
                                        type="checkbox"
                                        checked={checked}
                                        disabled={readOnly || locked}
                                        onChange={(e) =>
                                          handleToggleOther(act.code, act.super_admin_only, e.target.checked)
                                        }
                                        className="w-3.5 h-3.5 rounded cursor-pointer disabled:cursor-not-allowed"
                                      />
                                      <span className="text-[var(--cms-fg)]">{act.label}</span>
                                    </label>
                                  );
                                })}
                              </div>
                            </div>
                          );
                        })}
                      </div>
                    )}
                  </div>
                );
              })}
            </div>
          )}
        </div>
      </form>
    </CmsDialog>
  );
}
