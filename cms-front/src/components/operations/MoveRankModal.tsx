import React, { useEffect, useId, useState } from 'react';
import { CmsDialog } from '../ui/CmsDialog';
import type { RecommendationEntry } from '../../utils/api';

interface MoveRankModalProps {
  open: boolean;
  entry: RecommendationEntry | null;
  currentIndex: number;
  totalCount: number;
  onClose: () => void;
  onConfirm: (targetOneBasedRank: number) => void;
}

export const MoveRankModal: React.FC<MoveRankModalProps> = ({
  open,
  entry,
  currentIndex,
  totalCount,
  onClose,
  onConfirm,
}) => {
  const formId = useId();
  const [rankInput, setRankInput] = useState('1');
  const [errorMsg, setErrorMsg] = useState('');

  useEffect(() => {
    if (!open) return;
    setErrorMsg('');
    setRankInput(String(currentIndex + 1));
  }, [open, currentIndex]);

  const handleSubmit = (e: React.FormEvent) => {
    e.preventDefault();
    const parsed = Number(rankInput.trim());
    if (!Number.isInteger(parsed) || parsed < 1 || parsed > totalCount) {
      setErrorMsg(`请输入 1 至 ${totalCount} 之间的整数位次`);
      return;
    }
    onConfirm(parsed);
    onClose();
  };

  return (
    <CmsDialog
      open={open}
      onClose={onClose}
      variant="modal"
      size="sm"
      closedBy="any"
      title="调整展示位次"
      subtitle={entry?.novel?.title || (entry ? `Book #${entry.novel_id}` : '')}
      footer={
        <>
          <button type="button" onClick={onClose} className="btn-secondary">
            取消
          </button>
          <button type="submit" form={formId} className="btn-primary">
            确认调整
          </button>
        </>
      }
    >
      <form id={formId} onSubmit={handleSubmit} className="space-y-3 text-xs">
        <label className="block space-y-1.5">
          <span className="block font-medium text-[var(--cms-muted-fg)]">
            目标位次（当前第 {currentIndex + 1} 位，共 {totalCount} 本）
          </span>
          <input
            type="number"
            autoFocus
            min={1}
            max={Math.max(1, totalCount)}
            className="input-field tabular-nums"
            value={rankInput}
            onChange={(e) => {
              setErrorMsg('');
              setRankInput(e.target.value);
            }}
          />
        </label>
        {errorMsg && (
          <p role="alert" className="text-xs text-[var(--cms-error)] m-0">
            {errorMsg}
          </p>
        )}
      </form>
    </CmsDialog>
  );
};
