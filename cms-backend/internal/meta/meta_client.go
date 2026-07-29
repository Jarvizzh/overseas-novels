package meta

import (
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"net/url"
	"os"
	"strconv"
	"strings"
	"time"
)

const (
	// DefaultMetaGraphHost Meta Graph API 默认域名
	DefaultMetaGraphHost = "https://graph.facebook.com"
	// DefaultMetaAPIVersion Meta Graph API 默认版本号 (可通过环境变量 META_API_VERSION 覆盖)
	DefaultMetaAPIVersion = "v25.0"
	// DefaultPageLimit 默认分页单页拉取上限
	DefaultPageLimit = "500"
)

type MetaClient struct {
	AccessToken string
	BusinessID  string
	APIVersion  string
	HTTPClient  *http.Client
}

func NewMetaClient(token, bmID string, apiVersion ...string) *MetaClient {
	ver := "v25.0"
	if len(apiVersion) > 0 && apiVersion[0] != "" {
		ver = apiVersion[0]
	} else if envVer := os.Getenv("META_API_VERSION"); envVer != "" {
		ver = envVer
	}
	if !strings.HasPrefix(ver, "v") {
		ver = "v" + ver
	}

	return &MetaClient{
		AccessToken: token,
		BusinessID:  bmID,
		APIVersion:  ver,
		HTTPClient:  &http.Client{Timeout: 30 * time.Second},
	}
}

// getBaseURL 获取 API 请求基础前缀路径
func (c *MetaClient) getBaseURL() string {
	host := os.Getenv("META_GRAPH_HOST")
	if host == "" {
		host = DefaultMetaGraphHost
	}
	host = strings.TrimSuffix(host, "/")

	version := c.APIVersion
	if version == "" {
		version = os.Getenv("META_API_VERSION")
	}
	if version == "" {
		version = DefaultMetaAPIVersion
	}
	if !strings.HasPrefix(version, "v") {
		version = "v" + version
	}

	return fmt.Sprintf("%s/%s", host, version)
}

type ActionItem struct {
	ActionType string      `json:"action_type"`
	Value      interface{} `json:"value"`
}

func parseFloat(v interface{}) float64 {
	if v == nil {
		return 0
	}
	switch val := v.(type) {
	case float64:
		return val
	case float32:
		return float64(val)
	case int64:
		return float64(val)
	case int:
		return float64(val)
	case string:
		f, _ := strconv.ParseFloat(val, 64)
		return f
	default:
		f, _ := strconv.ParseFloat(fmt.Sprintf("%v", val), 64)
		return f
	}
}

func parseInt64(v interface{}) int64 {
	if v == nil {
		return 0
	}
	switch val := v.(type) {
	case float64:
		return int64(val)
	case int64:
		return val
	case int:
		return int64(val)
	case string:
		i, _ := strconv.ParseInt(val, 10, 64)
		return i
	default:
		i, _ := strconv.ParseInt(fmt.Sprintf("%v", val), 10, 64)
		return i
	}
}

func extractActionCount(actions []ActionItem, targetType string) int64 {
	for _, act := range actions {
		if act.ActionType == targetType {
			return parseInt64(act.Value)
		}
	}
	var total int64 = 0
	for _, act := range actions {
		if strings.HasSuffix(act.ActionType, "."+targetType) ||
			act.ActionType == "omni_"+targetType ||
			act.ActionType == "offsite_conversion.fb_pixel_"+targetType {
			total += parseInt64(act.Value)
		}
	}
	return total
}

func extractActionFloat(actions []ActionItem, targetType string) float64 {
	for _, act := range actions {
		if act.ActionType == targetType {
			return parseFloat(act.Value)
		}
	}
	var total float64 = 0
	for _, act := range actions {
		if strings.HasSuffix(act.ActionType, "."+targetType) ||
			act.ActionType == "omni_"+targetType ||
			act.ActionType == "offsite_conversion.fb_pixel_"+targetType {
			total += parseFloat(act.Value)
		}
	}
	return total
}

// FetchAccounts 获取 BM 及 Token 权限下所有广告账户
func (c *MetaClient) FetchAccounts() ([]AdAccount, error) {
	if c.AccessToken == "" {
		return nil, fmt.Errorf("Meta AccessToken is empty")
	}

	endpoints := []string{
		fmt.Sprintf("%s/me/adaccounts", c.getBaseURL()),
	}
	if c.BusinessID != "" {
		endpoints = append(endpoints,
			fmt.Sprintf("%s/%s/client_ad_accounts", c.getBaseURL(), c.BusinessID),
			fmt.Sprintf("%s/%s/owned_ad_accounts", c.getBaseURL(), c.BusinessID),
		)
	}

	accMap := make(map[string]AdAccount)

	for _, ep := range endpoints {
		params := url.Values{}
		params.Set("fields", "id,name,currency,timezone_name,account_status")
		params.Set("limit", DefaultPageLimit)
		params.Set("access_token", c.AccessToken)

		nextURL := ep + "?" + params.Encode()

		for nextURL != "" {
			respBytes, err := c.get(nextURL)
			if err != nil {
				log.Printf("[Meta API Client] Warning: endpoint %s error: %v", ep, err)
				break
			}

			var res struct {
				Data []struct {
					ID            string      `json:"id"`
					Name          string      `json:"name"`
					Currency      string      `json:"currency"`
					TimezoneName  string      `json:"timezone_name"`
					AccountStatus interface{} `json:"account_status"`
				} `json:"data"`
				Paging struct {
					Next string `json:"next"`
				} `json:"paging"`
			}

			if err := json.Unmarshal(respBytes, &res); err != nil {
				log.Printf("[Meta API Client] Warning: unmarshal error for %s: %v", ep, err)
				break
			}

			for _, acc := range res.Data {
				status := "ACTIVE"
				if parseInt64(acc.AccountStatus) != 1 {
					status = "PAUSED"
				}
				accID := acc.ID
				if !strings.HasPrefix(accID, "act_") {
					accID = "act_" + accID
				}
				accMap[accID] = AdAccount{
					ID:       accID,
					Name:     acc.Name,
					Currency: acc.Currency,
					Timezone: acc.TimezoneName,
					Status:   status,
					BmID:     c.BusinessID,
				}
			}
			nextURL = res.Paging.Next
		}
	}

	var accounts []AdAccount
	for _, acc := range accMap {
		accounts = append(accounts, acc)
	}
	return accounts, nil
}

// FetchCampaigns 获取广告系列列表
func (c *MetaClient) FetchCampaigns(accountID string) ([]Campaign, error) {
	endpoint := fmt.Sprintf("%s/%s/campaigns", c.getBaseURL(), accountID)
	params := url.Values{}
	params.Set("fields", "id,name,objective,effective_status")
	params.Set("limit", DefaultPageLimit)
	params.Set("access_token", c.AccessToken)
	nextURL := endpoint + "?" + params.Encode()

	var campaigns []Campaign
	for nextURL != "" {
		respBytes, err := c.get(nextURL)
		if err != nil {
			return nil, err
		}

		var res struct {
			Data []struct {
				ID              string `json:"id"`
				Name            string `json:"name"`
				Objective       string `json:"objective"`
				EffectiveStatus string `json:"effective_status"`
			} `json:"data"`
			Paging struct {
				Next string `json:"next"`
			} `json:"paging"`
		}

		if err := json.Unmarshal(respBytes, &res); err != nil {
			return nil, err
		}

		for _, camp := range res.Data {
			campaigns = append(campaigns, Campaign{
				ID:        camp.ID,
				AccountID: accountID,
				Name:      camp.Name,
				Objective: camp.Objective,
				Status:    camp.EffectiveStatus,
			})
		}
		nextURL = res.Paging.Next
	}
	return campaigns, nil
}

// FetchAdSets 获取广告组列表
func (c *MetaClient) FetchAdSets(accountID string) ([]AdSet, error) {
	endpoint := fmt.Sprintf("%s/%s/adsets", c.getBaseURL(), accountID)
	params := url.Values{}
	params.Set("fields", "id,name,campaign_id,daily_budget,effective_status")
	params.Set("limit", DefaultPageLimit)
	params.Set("access_token", c.AccessToken)
	nextURL := endpoint + "?" + params.Encode()

	var adSets []AdSet
	for nextURL != "" {
		respBytes, err := c.get(nextURL)
		if err != nil {
			return nil, err
		}

		var res struct {
			Data []struct {
				ID              string      `json:"id"`
				Name            string      `json:"name"`
				CampaignID      string      `json:"campaign_id"`
				DailyBudget     interface{} `json:"daily_budget"`
				EffectiveStatus string      `json:"effective_status"`
			} `json:"data"`
			Paging struct {
				Next string `json:"next"`
			} `json:"paging"`
		}

		if err := json.Unmarshal(respBytes, &res); err != nil {
			return nil, err
		}

		for _, item := range res.Data {
			adSets = append(adSets, AdSet{
				ID:          item.ID,
				CampaignID:  item.CampaignID,
				AccountID:   accountID,
				Name:        item.Name,
				DailyBudget: parseFloat(item.DailyBudget) / 100.0,
				Status:      item.EffectiveStatus,
			})
		}
		nextURL = res.Paging.Next
	}
	return adSets, nil
}

// FetchAds 获取广告列表
func (c *MetaClient) FetchAds(accountID string) ([]Ad, error) {
	endpoint := fmt.Sprintf("%s/%s/ads", c.getBaseURL(), accountID)
	params := url.Values{}
	params.Set("fields", "id,name,adset_id,campaign_id,effective_status,created_time")
	params.Set("limit", DefaultPageLimit)
	params.Set("access_token", c.AccessToken)
	nextURL := endpoint + "?" + params.Encode()

	var ads []Ad
	for nextURL != "" {
		respBytes, err := c.get(nextURL)
		if err != nil {
			return nil, err
		}

		var res struct {
			Data []struct {
				ID              string `json:"id"`
				Name            string `json:"name"`
				AdSetID         string `json:"adset_id"`
				CampaignID      string `json:"campaign_id"`
				EffectiveStatus string `json:"effective_status"`
				CreatedTime     string `json:"created_time"`
			} `json:"data"`
			Paging struct {
				Next string `json:"next"`
			} `json:"paging"`
		}

		if err := json.Unmarshal(respBytes, &res); err != nil {
			return nil, err
		}

		for _, item := range res.Data {
			var createdTime *time.Time
			if t, err := time.Parse(time.RFC3339, item.CreatedTime); err == nil {
				createdTime = &t
			}
			ads = append(ads, Ad{
				ID:          item.ID,
				AdSetID:     item.AdSetID,
				CampaignID:  item.CampaignID,
				AccountID:   accountID,
				Name:        item.Name,
				Status:      item.EffectiveStatus,
				CreatedTime: createdTime,
			})
		}
		nextURL = res.Paging.Next
	}

	return ads, nil
}

func parseDatePreset(preset string) string {
	switch preset {
	case "1d", "last_1d":
		return "today"
	case "3d", "last_3d":
		return "last_3d"
	case "7d", "last_7d":
		return "last_7d"
	case "14d", "last_14d", "15d", "last_15d":
		return "last_14d"
	case "30d", "last_30d":
		return "last_30d"
	default:
		if strings.HasPrefix(preset, "last_") {
			return preset
		}
		return "last_30d"
	}
}

// FetchInsights 真实 Meta API 统计报表拉取
func (c *MetaClient) FetchInsights(accountID string, level string, datePreset string) ([]DailyInsight, error) {
	endpoint := fmt.Sprintf("%s/%s/insights", c.getBaseURL(), accountID)
	params := url.Values{}
	params.Set("level", level)
	params.Set("time_increment", "1")
	params.Set("date_preset", parseDatePreset(datePreset))
	params.Set("limit", DefaultPageLimit)

	params.Set("fields", "ad_id,adset_id,campaign_id,account_id,spend,impressions,reach,frequency,cpm,clicks,cpc,ctr,purchase_roas,action_values,actions")
	params.Set("access_token", c.AccessToken)

	nextURL := endpoint + "?" + params.Encode()
	var insights []DailyInsight

	for nextURL != "" {
		respBytes, err := c.get(nextURL)
		if err != nil {
			return nil, err
		}

		var res struct {
			Data []struct {
				AdID         string       `json:"ad_id"`
				AdSetID      string       `json:"adset_id"`
				CampaignID   string       `json:"campaign_id"`
				AccountID    string       `json:"account_id"`
				DateStart    string       `json:"date_start"`
				Spend        interface{}  `json:"spend"`
				Impressions  interface{}  `json:"impressions"`
				Reach        interface{}  `json:"reach"`
				Frequency    interface{}  `json:"frequency"`
				CPM          interface{}  `json:"cpm"`
				Clicks       interface{}  `json:"clicks"`
				CPC          interface{}  `json:"cpc"`
				CTR          interface{}  `json:"ctr"`
				PurchaseRoas []ActionItem `json:"purchase_roas"`
				ActionValues []ActionItem `json:"action_values"`
				Actions      []ActionItem `json:"actions"`
			} `json:"data"`
			Paging struct {
				Next string `json:"next"`
			} `json:"paging"`
		}

		if err := json.Unmarshal(respBytes, &res); err != nil {
			return nil, err
		}

		for _, item := range res.Data {
			entityID := item.AccountID
			if level == "campaign" {
				entityID = item.CampaignID
			} else if level == "adset" {
				entityID = item.AdSetID
			} else if level == "ad" {
				entityID = item.AdID
			} else if level == "account" {
				if entityID != "" && !strings.HasPrefix(entityID, "act_") {
					entityID = "act_" + entityID
				}
				if entityID == "" {
					entityID = accountID
				}
			}

			if entityID == "" {
				continue
			}

			di := DailyInsight{
				ID:                          fmt.Sprintf("%s_%s_%s", level, entityID, item.DateStart),
				EntityLevel:                 level,
				EntityID:                    entityID,
				StatDate:                    item.DateStart,
				Spend:                       parseFloat(item.Spend),
				Impressions:                 parseInt64(item.Impressions),
				Reach:                       parseInt64(item.Reach),
				Frequency:                   parseFloat(item.Frequency),
				CPM:                         parseFloat(item.CPM),
				Clicks:                      parseInt64(item.Clicks),
				CPC:                         parseFloat(item.CPC),
				CTR:                         parseFloat(item.CTR),
				LinkClicks:                  extractActionCount(item.Actions, "link_click"),
				LandingPageViews:            extractActionCount(item.Actions, "landing_page_view"),
				ViewContentCount:            extractActionCount(item.Actions, "view_content"),
				AddToCartCount:              extractActionCount(item.Actions, "add_to_cart"),
				InitiateCheckoutCount:       extractActionCount(item.Actions, "initiate_checkout"),
				CompleteRegistrationCount:   extractActionCount(item.Actions, "complete_registration"),
				PurchaseCount:               extractActionCount(item.Actions, "purchase"),
				PurchaseValue:               extractActionFloat(item.ActionValues, "purchase"),
				PurchaseROAS:                extractActionFloat(item.PurchaseRoas, "purchase"),
			}

			if di.LinkClicks > 0 {
				di.CostPerLinkClick = di.Spend / float64(di.LinkClicks)
			}
			if di.Impressions > 0 {
				di.LinkCTR = (float64(di.LinkClicks) / float64(di.Impressions)) * 100.0
			}
			if di.LandingPageViews > 0 {
				di.CostPerLandingPageView = di.Spend / float64(di.LandingPageViews)
			}
			if di.AddToCartCount > 0 {
				di.CostPerAddToCart = di.Spend / float64(di.AddToCartCount)
			}
			if di.CompleteRegistrationCount > 0 {
				di.CostPerCompleteRegistration = di.Spend / float64(di.CompleteRegistrationCount)
			}
			if di.PurchaseCount > 0 {
				di.CostPerPurchase = di.Spend / float64(di.PurchaseCount)
			}
			if di.PurchaseROAS == 0 && di.Spend > 0 && di.PurchaseValue > 0 {
				di.PurchaseROAS = di.PurchaseValue / di.Spend
			}

			insights = append(insights, di)
		}

		nextURL = res.Paging.Next
	}

	log.Printf("[Meta API Client] Fetched total %d insight records for level %s", len(insights), level)
	return insights, nil
}

func (c *MetaClient) get(urlStr string) ([]byte, error) {
	resp, err := c.HTTPClient.Get(urlStr)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		bodyBytes, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("Meta API returned HTTP %d: %s", resp.StatusCode, string(bodyBytes))
	}
	return io.ReadAll(resp.Body)
}
