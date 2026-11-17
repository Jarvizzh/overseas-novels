package novel

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/jackc/pgx/v5"
)

func TestCMSPricingEndpointsPreserveInheritFreeAndExplicit(t *testing.T) {
	pool := publicationDB(t)
	ctx := context.Background()
	repo := NewNovelRepository()
	s := NewNovelService(repo, nil, fakeCoverStore{})
	handler := NewHandler(s)
	router := gin.New()
	router.POST("/novels", handler.CreateNovel)
	router.PUT("/novels/:id", handler.UpdateNovel)
	router.POST("/settings", handler.UpdateSettings)

	request := func(method, path, body string, want int) string {
		t.Helper()
		req := httptest.NewRequest(method, path, strings.NewReader(body))
		req.Header.Set("Content-Type", "application/json")
		w := httptest.NewRecorder()
		router.ServeHTTP(w, req)
		if w.Code != want {
			t.Fatalf("%s %s: got %d want %d: %s", method, path, w.Code, want, w.Body)
		}
		return w.Body.String()
	}
	payload := func(cost string) string {
		return fmt.Sprintf(`{"title":"Book","author":"Author","cover_url":"oss://covers/2026/11/abc.png",
			"status":"Ongoing","synopsis":"Synopsis","genres":["Romance"],"rating":4.5,
			"start_pay_chapter_index":1,"coin_cost_per_thousand":%s}`, cost)
	}

	const expectedGlobalCost = 321
	request("POST", "/settings", `{"global_coin_cost_per_thousand":"321"}`, http.StatusOK)
	var created Novel
	for _, cost := range []string{"null", "0", "125"} {
		body := request("POST", "/novels", payload(cost), http.StatusCreated)
		if err := json.Unmarshal([]byte(body), &created); err != nil {
			t.Fatal(err)
		}
		data, err := repo.GetNovel(ctx, created.ID)
		if err != nil || data == nil {
			t.Fatalf("novel missing: %+v %v", data, err)
		}
		expected := expectedGlobalCost
		if cost != "null" && data.CoinCostPerThousand != nil {
			expected = *data.CoinCostPerThousand
		}
		if expected != data.EffectiveCoinCostPerThousand ||
			(cost == "null") != (data.CoinCostPerThousand == nil) ||
			(cost == "0" && expected != 0) || (cost == "125" && expected != 125) {
			t.Fatalf("unexpected create pricing %s: %+v", cost, data)
		}
	}
	path := fmt.Sprintf("/novels/%d", created.ID)
	request("POST", "/novels", payload("-1"), http.StatusBadRequest)
	request("PUT", path, payload("-1"), http.StatusBadRequest)
	if _, err := s.BulkImportChapters(ctx, created.ID, []byte("Chapter 1: Intro\n"+strings.Repeat("x", 1000)), "single_txt", ""); err != nil {
		t.Fatal(err)
	}
	check := func(cost *int, effective int) {
		t.Helper()
		var dbCost *int
		var dbPrice int
		var dbPaid bool
		err := pool.QueryRow(ctx, `SELECT n.coin_cost_per_thousand, c.price, c.is_paid
			FROM novels n JOIN chapters c ON c.novel_id=n.id WHERE n.id=$1`, created.ID).Scan(&dbCost, &dbPrice, &dbPaid)
		if err != nil || (dbCost == nil) != (cost == nil) || (cost != nil && *cost != *dbCost) ||
			dbPrice != effective || dbPaid != (effective != 0) {
			t.Fatalf("wrong persisted pricing: cost=%v price=%d paid=%v err=%v", dbCost, dbPrice, dbPaid, err)
		}
	}
	zero := 0
	positive := 125
	request("PUT", path, payload("0"), http.StatusOK)
	check(&zero, 0)
	request("PUT", path, payload("null"), http.StatusOK)
	check(nil, expectedGlobalCost)
	request("PUT", path, payload("125"), http.StatusOK)
	check(&positive, 125)
	request("POST", "/settings", `{"global_coin_cost_per_thousand":"0","apply_coin_cost_to_all_novels":"true"}`, http.StatusOK)
	check(&zero, 0)
	request("POST", "/settings", `{"global_coin_cost_per_thousand":"125","apply_coin_cost_to_all_novels":"true"}`, http.StatusOK)
	check(&positive, 125)
	request("POST", "/settings", `{"global_coin_cost_per_thousand":"321","reset_novel_coin_cost":"true"}`, http.StatusOK)
	check(nil, expectedGlobalCost)
}

type rowsProbe struct {
	pgx.Rows
	returned bool
	scanErr  error
	iterErr  error
}

func (r *rowsProbe) Close() {}
func (r *rowsProbe) Next() bool {
	if r.returned {
		return false
	}
	r.returned = true
	return true
}
func (r *rowsProbe) Scan(dest ...any) error {
	*dest[0].(*string), *dest[1].(*string) = "global_coin_cost_per_thousand", "500"
	return r.scanErr
}
func (r *rowsProbe) Err() error { return r.iterErr }

type settingsProbe struct{ rows *rowsProbe }

func (p settingsProbe) Query(context.Context, string, ...any) (pgx.Rows, error) {
	return p.rows, nil
}

type settingsFailureRepo struct {
	NovelRepository
	err error
}

func (r settingsFailureRepo) GetSettings(context.Context) (map[string]string, error) {
	return nil, r.err
}
func (r settingsFailureRepo) GetSettingsTx(context.Context, pgx.Tx) (map[string]string, error) {
	return nil, r.err
}

func TestSettingsFailuresNeverPublishOrReprice(t *testing.T) {
	failure := errors.New("settings connection lost")
	for _, rows := range []*rowsProbe{{scanErr: failure}, {iterErr: failure}} {
		if configs, err := readSettings(context.Background(), settingsProbe{rows}); configs != nil || !errors.Is(err, failure) {
			t.Fatalf("partial settings escaped: %v %v", configs, err)
		}
	}
	repo := newFakeNovelRepo()
	repo.novels[1] = &Novel{ID: 1, Status: "Ongoing", StartPayChapterIndex: 1}
	s := NewNovelService(settingsFailureRepo{repo, failure}, nil, nil)
	if _, err := s.BulkImportChapters(context.Background(), 1, []byte("Chapter 1: Intro\nText"), "single_txt", ""); !errors.Is(err, failure) {
		t.Fatalf("import ignored failed settings query: %v", err)
	}
	if err := s.UpdateSettings(context.Background(), map[string]string{
		"global_start_pay_chapter_index": "2", "apply_start_pay_to_all_novels": "true",
	}); !errors.Is(err, failure) {
		t.Fatalf("bulk pricing ignored failed settings query: %v", err)
	}
	if len(repo.chapters[1]) != 0 || len(repo.settings) != 0 || repo.batchStartPayCalls != 0 {
		t.Fatal("settings failure allowed a write")
	}
}
