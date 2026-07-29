package meta

import (
	"context"
	"log"
	"sort"
	"sync"
	"time"
)

type Service struct {
	repo *Repository
}

func NewService(repo *Repository) *Service {
	return &Service{repo: repo}
}

func (s *Service) PurgeAllData(ctx context.Context) error {
	log.Println("[Meta Service] Purging all advertising data...")
	return s.repo.PurgeAllData(ctx)
}

func (s *Service) GetMetaClient(ctx context.Context) *MetaClient {
	token, _ := s.repo.GetConfig(ctx, "META_ACCESS_TOKEN")
	bmID, _ := s.repo.GetConfig(ctx, "META_BUSINESS_ID")
	apiVersion, _ := s.repo.GetConfig(ctx, "META_API_VERSION")
	if apiVersion == "" {
		apiVersion = "v25.0"
	}
	return NewMetaClient(token, bmID, apiVersion)
}

func (s *Service) GetConfig(ctx context.Context, key string) string {
	val, _ := s.repo.GetConfig(ctx, key)
	return val
}

func (s *Service) SetConfig(ctx context.Context, key, value string) error {
	return s.repo.SetConfig(ctx, key, value)
}

func (s *Service) SyncAllData(datePreset string) error {
	ctx := context.Background()
	if datePreset == "" {
		datePreset = "last_7d"
	}

	client := s.GetMetaClient(ctx)
	log.Printf("[Meta Service] Starting real Meta API data sync for preset: %s...", datePreset)

	accounts, err := client.FetchAccounts()
	if err != nil {
		log.Printf("[Meta Service] FetchAccounts error: %v", err)
		return err
	}

	for _, acc := range accounts {
		_ = s.repo.SaveAccount(ctx, acc)

		campaigns, err := client.FetchCampaigns(acc.ID)
		if err != nil {
			log.Printf("[Meta Service] FetchCampaigns error for %s: %v", acc.ID, err)
		} else {
			log.Printf("[Meta Service] Fetched %d campaigns for %s", len(campaigns), acc.ID)
		}
		for _, c := range campaigns {
			if err := s.repo.SaveCampaign(ctx, c); err != nil {
				log.Printf("[Meta Service] Error saving campaign %s: %v", c.ID, err)
			}
		}

		adSets, err := client.FetchAdSets(acc.ID)
		if err != nil {
			log.Printf("[Meta Service] FetchAdSets error for %s: %v", acc.ID, err)
		} else {
			log.Printf("[Meta Service] Fetched %d adsets for %s", len(adSets), acc.ID)
		}
		for _, adSet := range adSets {
			if err := s.repo.SaveAdSet(ctx, adSet); err != nil {
				log.Printf("[Meta Service] Error saving adSet %s: %v", adSet.ID, err)
			}
		}

		ads, err := client.FetchAds(acc.ID)
		if err != nil {
			log.Printf("[Meta Service] FetchAds error for %s: %v", acc.ID, err)
		} else {
			log.Printf("[Meta Service] Fetched %d ads for %s", len(ads), acc.ID)
		}
		for _, ad := range ads {
			if err := s.repo.SaveAd(ctx, ad); err != nil {
				log.Printf("[Meta Service] Error saving ad %s: %v", ad.ID, err)
			}
		}

		var wg sync.WaitGroup
		levels := []string{"account", "campaign", "adset", "ad"}

		for _, lvl := range levels {
			wg.Add(1)
			go func(accID, level string) {
				defer wg.Done()
				insights, err := client.FetchInsights(accID, level, datePreset)
				if err != nil {
					log.Printf("[Meta Service] Error fetching insights for %s at level %s: %v", accID, level, err)
					return
				}
				log.Printf("[Meta Service] Fetched %d insight records for level %s (account: %s)", len(insights), level, accID)
				for _, ins := range insights {
					_ = s.repo.SaveDailyInsight(ctx, ins)
				}
			}(acc.ID, lvl)
		}
		wg.Wait()
	}

	log.Printf("[Meta Service] Real Meta API data sync for %s completed successfully!", datePreset)
	return nil
}

func (s *Service) GetHierarchy(ctx context.Context, startDate, endDate string) ([]*HierarchyNode, error) {
	accounts, err := s.repo.GetAccounts(ctx)
	if err != nil {
		return nil, err
	}

	result := make([]*HierarchyNode, 0)

	for _, acc := range accounts {
		accNode := &HierarchyNode{
			ID:       acc.ID,
			Name:     acc.Name,
			Level:    "account",
			Status:   acc.Status,
			Currency: acc.Currency,
			Children: make([]*HierarchyNode, 0),
		}

		campaigns, _ := s.repo.GetCampaignsByAccountID(ctx, acc.ID)

		for _, camp := range campaigns {
			campNode := &HierarchyNode{
				ID:       camp.ID,
				Name:     camp.Name,
				Level:    "campaign",
				Status:   camp.Status,
				Children: make([]*HierarchyNode, 0),
			}

			adSets, _ := s.repo.GetAdSetsByCampaignID(ctx, camp.ID)

			for _, adSet := range adSets {
				adSetNode := &HierarchyNode{
					ID:       adSet.ID,
					Name:     adSet.Name,
					Level:    "adset",
					Status:   adSet.Status,
					Budget:   adSet.DailyBudget,
					Children: make([]*HierarchyNode, 0),
				}

				ads, _ := s.repo.GetAdsByAdSetID(ctx, adSet.ID)

				for _, ad := range ads {
					adNode := &HierarchyNode{
						ID:      ad.ID,
						Name:    ad.Name,
						Level:   "ad",
						Status:  ad.Status,
						Metrics: s.aggregateMetrics(ctx, "ad", ad.ID, startDate, endDate),
					}
					adSetNode.Children = append(adSetNode.Children, adNode)
				}

				adSetNode.Metrics = s.aggregateMetrics(ctx, "adset", adSet.ID, startDate, endDate)
				if (adSetNode.Metrics.Spend == 0 && adSetNode.Metrics.Clicks == 0) && len(adSetNode.Children) > 0 {
					rolled := s.rollupChildMetrics("adset", adSet.ID, adSetNode.Children)
					if rolled.Spend > 0 || rolled.Clicks > 0 {
						adSetNode.Metrics = rolled
					}
				}

				// Sort ads by spend descending
				sort.Slice(adSetNode.Children, func(i, j int) bool {
					return adSetNode.Children[i].Metrics.Spend > adSetNode.Children[j].Metrics.Spend
				})

				campNode.Children = append(campNode.Children, adSetNode)
			}

			campNode.Metrics = s.aggregateMetrics(ctx, "campaign", camp.ID, startDate, endDate)
			if (campNode.Metrics.Spend == 0 && campNode.Metrics.Clicks == 0) && len(campNode.Children) > 0 {
				rolled := s.rollupChildMetrics("campaign", camp.ID, campNode.Children)
				if rolled.Spend > 0 || rolled.Clicks > 0 {
					campNode.Metrics = rolled
				}
			}

			// Sort adsets by spend descending
			sort.Slice(campNode.Children, func(i, j int) bool {
				return campNode.Children[i].Metrics.Spend > campNode.Children[j].Metrics.Spend
			})

			accNode.Children = append(accNode.Children, campNode)
		}

		accNode.Metrics = s.aggregateMetrics(ctx, "account", acc.ID, startDate, endDate)
		if (accNode.Metrics.Spend == 0 && accNode.Metrics.Clicks == 0) && len(accNode.Children) > 0 {
			rolled := s.rollupChildMetrics("account", acc.ID, accNode.Children)
			if rolled.Spend > 0 || rolled.Clicks > 0 {
				accNode.Metrics = rolled
			}
		}

		// Sort campaigns by spend descending
		sort.Slice(accNode.Children, func(i, j int) bool {
			return accNode.Children[i].Metrics.Spend > accNode.Children[j].Metrics.Spend
		})

		result = append(result, accNode)
	}

	// Sort accounts by spend descending
	sort.Slice(result, func(i, j int) bool {
		return result[i].Metrics.Spend > result[j].Metrics.Spend
	})

	return result, nil
}

func (s *Service) rollupChildMetrics(level, entityID string, children []*HierarchyNode) DailyInsight {
	var agg DailyInsight
	agg.EntityLevel = level
	agg.EntityID = entityID

	for _, child := range children {
		m := child.Metrics
		agg.Spend += m.Spend
		agg.Impressions += m.Impressions
		agg.Reach += m.Reach
		agg.Clicks += m.Clicks
		agg.LinkClicks += m.LinkClicks
		agg.LandingPageViews += m.LandingPageViews
		agg.ViewContentCount += m.ViewContentCount
		agg.AddToCartCount += m.AddToCartCount
		agg.InitiateCheckoutCount += m.InitiateCheckoutCount
		agg.CompleteRegistrationCount += m.CompleteRegistrationCount
		agg.PurchaseCount += m.PurchaseCount
		agg.PurchaseValue += m.PurchaseValue
	}

	if agg.Reach > 0 {
		agg.Frequency = float64(agg.Impressions) / float64(agg.Reach)
	}
	if agg.Impressions > 0 {
		agg.CPM = (agg.Spend / float64(agg.Impressions)) * 1000.0
		agg.CTR = (float64(agg.Clicks) / float64(agg.Impressions)) * 100.0
		agg.LinkCTR = (float64(agg.LinkClicks) / float64(agg.Impressions)) * 100.0
	}
	if agg.Clicks > 0 {
		agg.CPC = agg.Spend / float64(agg.Clicks)
	}
	if agg.LinkClicks > 0 {
		agg.CostPerLinkClick = agg.Spend / float64(agg.LinkClicks)
	}
	if agg.LandingPageViews > 0 {
		agg.CostPerLandingPageView = agg.Spend / float64(agg.LandingPageViews)
	}
	if agg.AddToCartCount > 0 {
		agg.CostPerAddToCart = agg.Spend / float64(agg.AddToCartCount)
	}
	if agg.CompleteRegistrationCount > 0 {
		agg.CostPerCompleteRegistration = agg.Spend / float64(agg.CompleteRegistrationCount)
	}
	if agg.PurchaseCount > 0 {
		agg.CostPerPurchase = agg.Spend / float64(agg.PurchaseCount)
	}
	if agg.Spend > 0 {
		agg.PurchaseROAS = agg.PurchaseValue / agg.Spend
	}

	return agg
}

func (s *Service) aggregateMetrics(ctx context.Context, level, entityID, startDate, endDate string) DailyInsight {
	insights, _ := s.repo.GetInsights(ctx, level, entityID, startDate, endDate)

	var agg DailyInsight
	agg.EntityLevel = level
	agg.EntityID = entityID

	if len(insights) == 0 {
		return agg
	}

	for _, item := range insights {
		agg.Spend += item.Spend
		agg.Impressions += item.Impressions
		agg.Reach += item.Reach
		agg.Clicks += item.Clicks
		agg.LinkClicks += item.LinkClicks
		agg.LandingPageViews += item.LandingPageViews
		agg.ViewContentCount += item.ViewContentCount
		agg.AddToCartCount += item.AddToCartCount
		agg.InitiateCheckoutCount += item.InitiateCheckoutCount
		agg.CompleteRegistrationCount += item.CompleteRegistrationCount
		agg.PurchaseCount += item.PurchaseCount
		agg.PurchaseValue += item.PurchaseValue
	}

	if agg.Reach > 0 {
		agg.Frequency = float64(agg.Impressions) / float64(agg.Reach)
	}
	if agg.Impressions > 0 {
		agg.CPM = (agg.Spend / float64(agg.Impressions)) * 1000.0
		agg.CTR = (float64(agg.Clicks) / float64(agg.Impressions)) * 100.0
		agg.LinkCTR = (float64(agg.LinkClicks) / float64(agg.Impressions)) * 100.0
	}
	if agg.Clicks > 0 {
		agg.CPC = agg.Spend / float64(agg.Clicks)
	}
	if agg.LinkClicks > 0 {
		agg.CostPerLinkClick = agg.Spend / float64(agg.LinkClicks)
	}
	if agg.LandingPageViews > 0 {
		agg.CostPerLandingPageView = agg.Spend / float64(agg.LandingPageViews)
	}
	if agg.AddToCartCount > 0 {
		agg.CostPerAddToCart = agg.Spend / float64(agg.AddToCartCount)
	}
	if agg.CompleteRegistrationCount > 0 {
		agg.CostPerCompleteRegistration = agg.Spend / float64(agg.CompleteRegistrationCount)
	}
	if agg.PurchaseCount > 0 {
		agg.CostPerPurchase = agg.Spend / float64(agg.PurchaseCount)
	}
	if agg.Spend > 0 {
		agg.PurchaseROAS = agg.PurchaseValue / agg.Spend
	}

	return agg
}

func (s *Service) GetOverview(ctx context.Context, startDate, endDate string) (OverviewResult, error) {
	insights, err := s.repo.GetInsightsByLevel(ctx, "account", startDate, endDate)
	if err != nil {
		return OverviewResult{}, err
	}

	res := OverviewResult{
		DailyTrend: make([]DailyTrendItem, 0),
	}
	trendMap := make(map[string]*DailyTrendItem)

	for _, item := range insights {
		res.TotalSpend += item.Spend
		res.TotalRevenue += item.PurchaseValue
		res.TotalImpressions += item.Impressions
		res.TotalReach += item.Reach
		res.TotalClicks += item.Clicks
		res.TotalLinkClicks += item.LinkClicks
		res.TotalLandingPageViews += item.LandingPageViews
		res.TotalAddToCart += item.AddToCartCount
		res.TotalRegistration += item.CompleteRegistrationCount
		res.TotalPurchases += item.PurchaseCount

		if _, ok := trendMap[item.StatDate]; !ok {
			trendMap[item.StatDate] = &DailyTrendItem{StatDate: item.StatDate}
		}
		trendMap[item.StatDate].Spend += item.Spend
		trendMap[item.StatDate].Revenue += item.PurchaseValue
		trendMap[item.StatDate].AddToCart += item.AddToCartCount
		trendMap[item.StatDate].Purchases += item.PurchaseCount
	}

	if res.TotalSpend == 0 {
		allInsights, _ := s.repo.GetInsightsByLevel(ctx, "ad", startDate, endDate)
		for _, item := range allInsights {
			res.TotalSpend += item.Spend
			res.TotalRevenue += item.PurchaseValue
			res.TotalImpressions += item.Impressions
			res.TotalReach += item.Reach
			res.TotalClicks += item.Clicks
			res.TotalAddToCart += item.AddToCartCount
			res.TotalRegistration += item.CompleteRegistrationCount
			res.TotalPurchases += item.PurchaseCount

			if _, ok := trendMap[item.StatDate]; !ok {
				trendMap[item.StatDate] = &DailyTrendItem{StatDate: item.StatDate}
			}
			trendMap[item.StatDate].Spend += item.Spend
			trendMap[item.StatDate].Revenue += item.PurchaseValue
			trendMap[item.StatDate].AddToCart += item.AddToCartCount
			trendMap[item.StatDate].Purchases += item.PurchaseCount
		}
	}

	if res.TotalSpend > 0 {
		res.AverageROAS = res.TotalRevenue / res.TotalSpend
	}

	for day := 30; day >= 0; day-- {
		dStr := time.Now().AddDate(0, 0, -day).Format("2006-01-02")
		if item, ok := trendMap[dStr]; ok {
			if item.Spend > 0 {
				item.ROAS = item.Revenue / item.Spend
			}
			res.DailyTrend = append(res.DailyTrend, *item)
		}
	}

	return res, nil
}
