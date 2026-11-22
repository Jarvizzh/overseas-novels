package wallet

import (
	"context"
	"errors"

	"reader-backend/internal/model"
	"reader-backend/internal/novel"
)

var (
	ErrPriceChanged        = errors.New("Chapter or price changed. Refresh before confirming.")
	ErrChapterNotFound     = errors.New("chapter not found")
	ErrPurchaseNeedsReview = errors.New("An older chapter purchase for this book needs review. Contact support before buying another chapter.")
)

type Service interface {
	GetWallet(ctx context.Context, userID int64) (*model.Wallet, error)
	GetHistory(ctx context.Context, userID int64, limit, offset int) ([]model.Transaction, error)
	GetHistoryPage(ctx context.Context, userID int64, limit int, after *historyCursor) (*HistoryPage, error)
	UnlockChapter(ctx context.Context, userID int64, novelID int64, chapterIndex int, promoID int, utmSource, utmCampaign string, quoted ChapterQuote) error
	CheckIn(context.Context, int64, bool) (*CheckInState, error)
}

type service struct {
	repo      Repository
	novelRepo novel.Repository
}

func NewService(repo Repository, novelRepo novel.Repository) Service {
	return &service{repo: repo, novelRepo: novelRepo}
}

func (s *service) GetWallet(ctx context.Context, userID int64) (*model.Wallet, error) {
	w, err := s.repo.GetWallet(ctx, userID)
	if err != nil {
		return nil, err
	}
	if w == nil {
		err = s.repo.AddCoins(ctx, userID, 0, false, "recharge", "Wallet Initialized")
		if err != nil {
			return nil, err
		}
		return s.repo.GetWallet(ctx, userID)
	}
	return w, nil
}

func (s *service) GetHistory(ctx context.Context, userID int64, limit, offset int) ([]model.Transaction, error) {
	return s.repo.GetTransactions(ctx, userID, limit, offset)
}

type ChapterQuote struct {
	ID      string
	Version int64
	Price   int
}

func (s *service) UnlockChapter(ctx context.Context, userID int64, novelID int64, chapterIndex int, promoID int, utmSource, utmCampaign string, quoted ChapterQuote) error {
	n, err := s.novelRepo.GetByID(ctx, novelID)
	if err != nil {
		return err
	}
	if !novel.IsNovelPublished(n) {
		return novel.ErrNovelNotFound
	}

	ch, err := s.novelRepo.GetChapter(ctx, novelID, chapterIndex)
	if err != nil {
		return err
	}
	if ch == nil {
		return ErrChapterNotFound
	}

	effectiveStartPay, effectiveCost, err := s.novelRepo.GetEffectivePricingRule(ctx, userID, novelID, promoID, utmSource, utmCampaign)
	if err != nil {
		return err
	}

	price := 0
	if chapterIndex >= effectiveStartPay-1 {
		price = novel.CalculateChapterPrice(ch.WordCount, effectiveCost)
	}
	quote := ChapterQuote{ID: ch.ID, Version: n.ContentVersion, Price: price}
	if quote.ID == "" || quoted != quote {
		return ErrPriceChanged
	}
	if effectiveCost == 0 || chapterIndex < effectiveStartPay-1 {
		return nil
	}
	hasPass, err := novel.HasReadingPass(ctx, s.novelRepo, userID, novelID)
	if err != nil || hasPass {
		return err
	}
	if err := s.repo.UnlockChapterTx(ctx, userID, novelID, chapterIndex, promoID, quote); err != nil {
		return err
	}
	return nil
}

func (s *service) CheckIn(ctx context.Context, userID int64, claim bool) (*CheckInState, error) {
	return s.repo.CheckIn(ctx, userID, claim)
}
