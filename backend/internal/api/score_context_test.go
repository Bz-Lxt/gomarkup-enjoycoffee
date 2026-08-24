package api_test

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"

	"github.com/alkaid/enjoycoffee/internal/api"
	"github.com/alkaid/enjoycoffee/internal/config"
	"github.com/alkaid/enjoycoffee/internal/flavorscore"
)

type blockingScoreRepository struct {
	entered chan struct{}
	release chan struct{}
	once    sync.Once

	mu    sync.Mutex
	score *flavorscore.Score
}

var _ flavorscore.Repository = (*blockingScoreRepository)(nil)

func newBlockingScoreRepository() *blockingScoreRepository {
	return &blockingScoreRepository{
		entered: make(chan struct{}),
		release: make(chan struct{}),
		score: &flavorscore.Score{
			ID:             1,
			BrewID:         41,
			BeanID:         7,
			AcidityX10:     40,
			SweetX10:       45,
			AromaX10:       50,
			AftertoneX10:   55,
			BodyX10:        60,
			BitterX10:      35,
			Note:           "取消前的评分",
		},
	}
}

func (r *blockingScoreRepository) Upsert(ctx context.Context, score *flavorscore.Score) (int64, error) {
	r.once.Do(func() { close(r.entered) })
	select {
	case <-ctx.Done():
		return 0, ctx.Err()
	case <-r.release:
	}
	if err := ctx.Err(); err != nil {
		return 0, err
	}

	stored := *score
	stored.ID = 1
	stored.BeanID = 7
	r.mu.Lock()
	r.score = &stored
	r.mu.Unlock()
	return stored.ID, nil
}

func (r *blockingScoreRepository) GetByBrew(ctx context.Context, brewID int64) (*flavorscore.Score, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.score == nil || r.score.BrewID != brewID {
		return nil, nil
	}
	copy := *r.score
	return &copy, nil
}

func (r *blockingScoreRepository) ListByBrews(context.Context, []int64) (map[int64]*flavorscore.Score, error) {
	return map[int64]*flavorscore.Score{}, nil
}

func (r *blockingScoreRepository) ListByBeanWithTime(context.Context, int64) ([]*flavorscore.Score, error) {
	return nil, nil
}

func (r *blockingScoreRepository) ListByBeans(context.Context, []int64) (map[int64][]*flavorscore.Score, error) {
	return map[int64][]*flavorscore.Score{}, nil
}

func (r *blockingScoreRepository) Delete(context.Context, int64) error { return nil }

func TestCanceledScoreSaveDoesNotTakeEffect(t *testing.T) {
	repo := newBlockingScoreRepository()
	scores := flavorscore.NewService(repo)
	cfg := config.Config{CORSOrigins: []string{"http://localhost"}}
	handlers := api.NewHandlers(cfg, nil, nil, nil, nil, scores, nil, nil, nil)
	router := handlers.Router()

	body := []byte(`{
		"acidity_x10": 80,
		"sweet_x10": 75,
		"aroma_x10": 70,
		"aftertone_x10": 65,
		"body_x10": 60,
		"bitter_x10": 25,
		"note": "锁释放后不应保存"
	}`)
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	req := httptest.NewRequest(http.MethodPut, "/api/v1/brews/41/score", bytes.NewReader(body)).WithContext(ctx)
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	done := make(chan struct{})
	go func() {
		defer close(done)
		router.ServeHTTP(rec, req)
	}()

	select {
	case <-repo.entered:
	case <-time.After(time.Second):
		t.Fatal("评分保存没有进入持久化阶段")
	}
	cancel()
	close(repo.release)

	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("请求取消后评分保存仍未返回")
	}

	getReq := httptest.NewRequest(http.MethodGet, "/api/v1/brews/41/score", nil)
	getRec := httptest.NewRecorder()
	router.ServeHTTP(getRec, getReq)
	if getRec.Code != http.StatusOK {
		t.Fatalf("查询评分状态码 = %d，响应 = %s", getRec.Code, getRec.Body.String())
	}

	var response struct {
		OK   bool `json:"ok"`
		Data struct {
			Score *flavorscore.View `json:"score"`
		} `json:"data"`
	}
	if err := json.Unmarshal(getRec.Body.Bytes(), &response); err != nil {
		t.Fatalf("解析查询响应失败: %v", err)
	}
	if !response.OK {
		t.Fatalf("查询评分失败: %s", getRec.Body.String())
	}
	if response.Data.Score == nil {
		t.Fatal("取消保存后原评分不应消失")
	}
	if response.Data.Score.AcidityX10 != 40 || response.Data.Score.SweetX10 != 45 ||
		response.Data.Score.Note != "取消前的评分" {
		t.Fatalf("已取消的保存覆盖了原评分: %+v", response.Data.Score)
	}
}
