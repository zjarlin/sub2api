package service

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/Wei-Shaw/sub2api/internal/jev_api"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
	"github.com/tidwall/gjson"
)

const autoContinueRecommendedInput = `{"model":"text-model","input":"请修复这个问题并验证，遇到选择直接按推荐来做，无需询问。","store":false,"tools":[{"type":"function","name":"exec_command","parameters":{"type":"object"}},{"type":"function","name":"request_user_input_async","parameters":{"type":"object"}}],"stream":false}`
const autoContinueNextStepStall = "已完成第一步改动。下一步可以按推荐继续：\n1. 补充回归测试并运行（推荐）\n2. 先重写整个模块\n请确认是否继续。"

// autoContinueSystemOneStub 模拟本机 System One 判据入口。
// 判据模型现在是并行提问，所以这里按实际返回的模型记录（并发安全），
// 断言要看「问到了哪些模型」而不是串行顺序。
type autoContinueSystemOneCalls struct {
	mu     sync.Mutex
	models []string
}

func (c *autoContinueSystemOneCalls) add(model string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.models = append(c.models, model)
}

func (c *autoContinueSystemOneCalls) load() []string {
	c.mu.Lock()
	defer c.mu.Unlock()
	return append([]string(nil), c.models...)
}

func autoContinueSystemOneStub(t *testing.T, handler func(model string, body []byte) (int, string)) (*OpenAIGatewayService, *Account, *autoContinueSystemOneCalls) {
	t.Helper()
	calls := &autoContinueSystemOneCalls{}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		require.Equal(t, "/v1/systemone", r.URL.Path)
		require.Equal(t, "Bearer auto-continue-test-key", r.Header.Get("Authorization"))
		body, err := io.ReadAll(r.Body)
		require.NoError(t, err)
		model := gjson.GetBytes(body, "model").String()
		calls.add(model)
		status, payload := handler(model, body)
		w.WriteHeader(status)
		_, _ = w.Write([]byte(payload))
	}))
	t.Cleanup(server.Close)
	cfg := visionTestConfig()
	cfg.Gateway.AutoContinue = config.GatewayAutoContinueConfig{Enabled: true, DecisionModel: "laya", MaxRounds: 2, TimeoutSeconds: 1}
	cfg.Server.Port = server.Listener.Addr().(*net.TCPAddr).Port
	cfg.JWT.Secret = "auto-continue-test-secret"
	primary := visionTestAccount(1, "text-model", "text")
	// 决策记录是续跑的前提：没有存储时网关会保留原停顿，不再静默续跑。
	svc := &OpenAIGatewayService{cfg: cfg, usageLogRepo: &autoContinueTestStore{}}
	return svc, &primary, calls
}

// visionSSEResponse 构造一条完整的 Responses 流，用于验证流式回合中的内部规划调用。
func visionSSEResponse(t *testing.T, model, id string, item map[string]any) *http.Response {
	t.Helper()
	response := map[string]any{"id": id, "object": "response", "model": model, "status": "completed", "output": []any{item}, "usage": map[string]int{"input_tokens": 10, "output_tokens": 5}}
	events := []map[string]any{
		{"type": "response.created", "sequence_number": 0, "response": map[string]any{"id": id, "model": model, "status": "in_progress"}},
		{"type": "response.output_item.added", "sequence_number": 1, "output_index": 0, "item": item},
		{"type": "response.output_item.done", "sequence_number": 2, "output_index": 0, "item": item},
		{"type": "response.completed", "sequence_number": 3, "response": response},
	}
	var stream strings.Builder
	for _, event := range events {
		encoded, err := json.Marshal(event)
		require.NoError(t, err)
		fmt.Fprintf(&stream, "event: %s\ndata: %s\n\n", event["type"], encoded)
	}
	return &http.Response{StatusCode: http.StatusOK, Header: http.Header{"Content-Type": {"text/event-stream"}}, Body: io.NopCloser(strings.NewReader(stream.String()))}
}

func autoContinueAnswers(action, basis string) string {
	answers := map[string]any{
		"action": map[string]any{"type": "choice", "choice": action, "probabilities": map[string]float64{action: 0.97}},
		"basis":  map[string]any{"type": "choice", "choice": basis, "probabilities": map[string]float64{basis: 0.97}},
		// 带选择题时必须同时给出选项，否则判据按「回答不完整」处理。
		"selection_0": map[string]any{"type": "choice", "choice": "1", "probabilities": map[string]float64{"1": 0.97}},
	}
	encoded, _ := json.Marshal(map[string]any{"model": "laya", "answers": answers})
	return string(encoded)
}

func TestAutoContinueUsesRecommendedOptionWithoutAsking(t *testing.T) {
	svc, account, calls := autoContinueSystemOneStub(t, func(string, []byte) (int, string) {
		return 500, `{"error":{"message":"explicit recommendation must not call the decision model"}}`
	})
	upstreamCalls := 0
	svc.httpUpstream = &codexModelsHTTPUpstreamStub{do: func(r *http.Request, _ string, _ int64, _ int) (*http.Response, error) {
		upstreamCalls++
		body, err := io.ReadAll(r.Body)
		require.NoError(t, err)
		if upstreamCalls == 1 {
			return visionTestResponse("text-model", "已复现适配器问题。需要你定一件事，请选：\n1. 按已确认设计迁移入口（推荐），保留旧链接\n2. 两个入口分别重写\n3. 两者都做\n选定后我立刻实施。"), nil
		}
		if strings.HasPrefix(gjson.GetBytes(body, "messages.0.content").String(), autoContinueTodoPlanPromptPrefix) {
			return visionTestResponse("text-model", `{"todos":["迁移入口","运行回归测试"]}`), nil
		}
		require.Contains(t, string(body), autoContinueMarker)
		require.Contains(t, string(body), `\"selection_0\":\"1\"`)
		return visionTestResponse("text-model", "已按推荐完成并验证"), nil
	}}
	c, recorder := autoContinueTestContext([]byte(autoContinueTestInput), 9, 7)
	_, err := svc.Forward(context.Background(), c, account, []byte(autoContinueTestInput))
	require.NoError(t, err)
	require.Empty(t, calls.load())
	require.Contains(t, recorder.Body.String(), "已按推荐完成并验证")
	require.NotContains(t, recorder.Body.String(), "选定后我立刻实施")
}

// autoContinueTestState 用真实解析路径构造判据 state，确保“用户是否允许”的判断基于同一份上下文。
func autoContinueTestState(t *testing.T, body []byte) map[string]any {
	t.Helper()
	candidate := &autoContinueCandidate{Text: "方案一（推荐）", Questions: []autoContinueQuestion{{ID: "selection_0", Prompt: "怎么做", Options: map[string]string{"1": "方案一（推荐）", "2": "方案二"}}}}
	state, ok := autoContinueDecisionState(body, candidate)
	require.True(t, ok)
	return state
}

func TestAutoContinueRecommendedRespectsAnalysisOnlyRequest(t *testing.T) {
	body := strings.Replace(autoContinueTestInput, "请修复这个问题并验证，按已确认的设计选择推荐方案。", "只分析原因，不要修改代码。", 1)
	if autoContinueAdoptRecommendation(autoContinueTestState(t, []byte(body)), []byte(body), &autoContinueCandidate{Questions: []autoContinueQuestion{{ID: "selection_0", Prompt: "怎么做", Options: map[string]string{"1": "方案一（推荐）", "2": "方案二"}}}}) != nil {
		t.Fatal("analysis-only requests must not auto-select a recommended implementation")
	}
}

func TestAutoContinueNewProblemRequiresArbitrationUnlessRecommendationExplicit(t *testing.T) {
	question := &autoContinueCandidate{Questions: []autoContinueQuestion{{ID: "selection_0", Prompt: "怎么做", Options: map[string]string{"1": "方案一（推荐）", "2": "方案二"}}}}
	body := strings.Replace(autoContinueTestInput, "请修复这个问题并验证，按已确认的设计选择推荐方案。", "先修这个问题。另外还有一个新需求需要额外取舍。", 1)
	require.Nil(t, autoContinueAdoptRecommendation(autoContinueTestState(t, []byte(body)), []byte(body), question))
	explicit := strings.Replace(autoContinueTestInput, "请修复这个问题并验证，按已确认的设计选择推荐方案。", "修复这个新需求，直接按推荐方案来做。", 1)
	require.NotNil(t, autoContinueAdoptRecommendation(autoContinueTestState(t, []byte(explicit)), []byte(explicit), question))
}

func TestAutoContinueSystemOneDecisionStopsOnDone(t *testing.T) {
	svc, account, calls := autoContinueSystemOneStub(t, func(string, []byte) (int, string) {
		encoded, _ := json.Marshal(map[string]any{"model": "laya", "answers": map[string]any{
			"action": map[string]any{"type": "choice", "choice": "done", "probabilities": map[string]float64{"done": 0.99}},
		}})
		return http.StatusOK, string(encoded)
	})
	upstreamCalls := 0
	svc.httpUpstream = &codexModelsHTTPUpstreamStub{do: func(_ *http.Request, _ string, _ int64, _ int) (*http.Response, error) {
		upstreamCalls++
		return visionTestResponse("text-model", "已复现，适配器旧接口返回404，尚未修复，需要修正协议。"), nil
	}}
	c, recorder := autoContinueTestContext([]byte(autoContinueTestInput), 9, 7)
	_, err := svc.Forward(context.Background(), c, account, []byte(autoContinueTestInput))
	require.NoError(t, err)
	// DecisionModel 是 laya：并行提问后 laya 判定 done 即停止续跑。
	// JEV 可能还没来得及记录请求就被取消，这里只断言高优先级判据确实被问到。
	require.Contains(t, calls.load(), "laya")
	require.Equal(t, 1, upstreamCalls)
	require.Contains(t, recorder.Body.String(), "适配器旧接口返回404")
}

func TestAutoContinueSystemOneContinuesAndRecordsTodos(t *testing.T) {
	svc, account, calls := autoContinueSystemOneStub(t, func(string, []byte) (int, string) {
		return http.StatusOK, autoContinueAnswers("continue", "repair")
	})
	plans := 0
	continued := 0
	svc.httpUpstream = &codexModelsHTTPUpstreamStub{do: func(r *http.Request, _ string, _ int64, _ int) (*http.Response, error) {
		body, err := io.ReadAll(r.Body)
		require.NoError(t, err)
		if strings.Contains(string(body), autoContinueTodoMarker) {
			continued++
			return visionTestResponse("text-model", "已修复并验证"), nil
		}
		plans++
		if plans == 1 {
			return visionTestResponse("text-model", "已复现适配器问题，尚未修复。"), nil
		}
		return visionTestResponse("text-model", `{"todos":["修正适配器协议返回码","运行 go test ./internal/service/ 验证回归"]}`), nil
	}}
	store := &autoContinueTestStore{}
	svc.usageLogRepo = store
	c, recorder := autoContinueTestContext([]byte(autoContinueRecommendedInput), 9, 7)
	_, err := svc.Forward(context.Background(), c, account, []byte(autoContinueRecommendedInput))
	require.NoError(t, err)
	// laya 是最高优先级：它给出结论后不再等待 JEV，JEV 的请求可能被提前取消。
	require.Contains(t, calls.load(), "laya")
	require.Equal(t, 1, continued)
	require.Contains(t, recorder.Body.String(), "已修复并验证")
	require.Len(t, store.routes, 1)
	require.Equal(t, "repair", store.routes[0].Operation.Decision.Source)
	require.Equal(t, []string{"修正适配器协议返回码", "运行 go test ./internal/service/ 验证回归"}, store.routes[0].Operation.Decision.Todo)
	// 执行轮 + TODO 规划轮都要计费，不能当作免费内部调用。
	require.NotEmpty(t, TakeVisionFallbackUsage(c))
}

func TestAutoContinueSystemOneUnavailableFallsBackToHighestTier(t *testing.T) {
	svc, account, calls := autoContinueSystemOneStub(t, func(model string, _ []byte) (int, string) {
		return http.StatusServiceUnavailable, fmt.Sprintf(`{"error":{"message":"No available laya account for %s"}}`, model)
	})
	judge := visionTestAccount(2, "gpt-6-astra", "text")
	svc.accountRepo = codexModelsVisibilityAccountRepo{byGroup: map[int64][]Account{7: {*account, judge}}}
	var ids []int64
	svc.httpUpstream = &codexModelsHTTPUpstreamStub{do: func(r *http.Request, _ string, id int64, _ int) (*http.Response, error) {
		body, readErr := io.ReadAll(r.Body)
		require.NoError(t, readErr)
		// 规划轮不再计入执行轮次，单独返回 JSON 清单。
		if strings.HasPrefix(gjson.GetBytes(body, "messages.0.content").String(), autoContinueTodoPlanPromptPrefix) {
			return visionTestResponse(account.Name, `{"todos":["修正适配器协议","运行回归测试"]}`), nil
		}
		ids = append(ids, id)
		if id == judge.ID {
			return visionTestResponse(judge.Name, `{"action":"continue","selections":{"selection_0":"1"},"plan":"按已确认设计迁移入口并验证","reason":"原始请求要求修复"}`), nil
		}
		if len(ids) == 1 {
			// 没有唯一推荐的停顿：判据都不可用后由最高档仲裁确认继续。
			return visionTestResponse(account.Name, autoContinueNoRecommendationQuestion), nil
		}
		require.Contains(t, string(body), autoContinueMarker)
		return visionTestResponse(account.Name, "已修复并验证"), nil
	}}
	c, recorder := autoContinueTestContext([]byte(autoContinueTestInput), 9, 7)
	_, err := svc.Forward(context.Background(), c, account, []byte(autoContinueTestInput))
	require.NoError(t, err)
	// DecisionModel 已是 laya，去重后只在 laya 与 JEV 各试一次（并行），都不可用才走最高档仲裁。
	require.ElementsMatch(t, []string{"laya", "typesafe/jev"}, calls.load())
	require.Equal(t, []int64{account.ID, judge.ID, account.ID}, ids)
	require.Contains(t, recorder.Body.String(), "已修复并验证")
}

// autoContinuePriorityStub 构造一个可按模型延迟/失败的 System One 判据入口。
func autoContinuePriorityStub(t *testing.T, handler func(model string) (int, string)) (*OpenAIGatewayService, *autoContinueCandidate, *gin.Context) {
	t.Helper()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, err := io.ReadAll(r.Body)
		require.NoError(t, err)
		status, payload := handler(gjson.GetBytes(body, "model").String())
		w.WriteHeader(status)
		_, _ = w.Write([]byte(payload))
	}))
	t.Cleanup(server.Close)
	cfg := visionTestConfig()
	cfg.Gateway.AutoContinue = config.GatewayAutoContinueConfig{Enabled: true, DecisionModel: "typesafe/jev", MaxRounds: 2, TimeoutSeconds: 1, SystemOneTimeoutSeconds: 8}
	// 统一判据选择：无推荐时由判据给出唯一选项，避免回答不完整。

	cfg.Server.Port = server.Listener.Addr().(*net.TCPAddr).Port
	cfg.JWT.Secret = "auto-continue-test-secret"
	svc := &OpenAIGatewayService{cfg: cfg, usageLogRepo: &autoContinueTestStore{}}
	// 只有唯一推荐缺失时才会询问判据，这里用不含推荐的停顿制造该路径。
	candidate := &autoContinueCandidate{Text: autoContinueNoRecommendationQuestion, Questions: []autoContinueQuestion{{
		ID: "selection_0", Prompt: "怎么做", Options: map[string]string{"1": "方案一", "2": "方案二"}}}}
	c, _ := autoContinueTestContext([]byte(autoContinueTestInput), 9, 7)
	return svc, candidate, c
}

func TestAutoContinueSystemOnePrefersConfiguredModelOverFasterFallback(t *testing.T) {
	svc, candidate, c := autoContinuePriorityStub(t, func(model string) (int, string) {
		if model == jev_api.ModelID {
			time.Sleep(300 * time.Millisecond)
			return http.StatusOK, autoContinueAnswers("continue", "design")
		}
		// 低优先级 laya 极快返回，但不能覆盖显式配置的 JEV。
		return http.StatusOK, autoContinueAnswers("continue", "repair")
	})
	// 用不含推荐标记的停顿走到判据路径。
	decision, answered := svc.askAutoContinueSystemOne(context.Background(), c, []byte(autoContinueTestInput), candidate)
	require.True(t, answered)
	require.NotNil(t, decision)
	require.Equal(t, "design", decision.Source)
}

func TestAutoContinueSystemOneFallsBackAfterPriorityGrace(t *testing.T) {
	svc, candidate, c := autoContinuePriorityStub(t, func(model string) (int, string) {
		if model == jev_api.ModelID {
			// 显式配置的 JEV 卡住超过宽限期但仍在总预算内。
			time.Sleep(autoContinueSystemOnePriorityGrace + 800*time.Millisecond)
			return http.StatusOK, autoContinueAnswers("done", "design")
		}
		return http.StatusOK, autoContinueAnswers("continue", "repair")
	})
	started := time.Now()
	decision, answered := svc.askAutoContinueSystemOne(context.Background(), c, []byte(autoContinueTestInput), candidate)
	elapsed := time.Since(started)
	require.True(t, answered)
	require.NotNil(t, decision)
	require.Equal(t, "repair", decision.Source)
	require.Equal(t, jev_api.LayaModelID, decision.Model)
	// 宽限期后即采用低优先级答案，不等到 JEV 完成，更不能等到总预算。
	require.Less(t, elapsed, autoContinueSystemOnePriorityGrace+700*time.Millisecond)
}

func TestAutoContinueSystemOneRespectsTotalBudgetWhenAllUnavailable(t *testing.T) {
	svc, candidate, c := autoContinuePriorityStub(t, func(string) (int, string) {
		time.Sleep(5 * time.Second)
		return http.StatusServiceUnavailable, `{"error":{"message":"unavailable"}}`
	})
	svc.cfg.Gateway.AutoContinue.SystemOneTimeoutSeconds = 1
	started := time.Now()
	_, answered := svc.askAutoContinueSystemOne(context.Background(), c, []byte(autoContinueTestInput), candidate)
	elapsed := time.Since(started)
	require.False(t, answered)
	require.Less(t, elapsed, 3*time.Second, "判据不可用时必须按总预算返回，不能串行等待")
}

func TestAutoContinueFallbackTodosSkipRecommendedOptions(t *testing.T) {
	todos := autoContinueFallbackTodos(autoContinueNextStepStall, 8)
	require.Equal(t, []string{"先重写整个模块"}, todos)
	text := "下一步：\n1. 补充回归测试并运行\n2. 先重写模块（推荐方案）\n3. 更新文档"
	require.Equal(t, []string{"补充回归测试并运行", "更新文档"}, autoContinueFallbackTodos(text, 8))
}

func TestAutoContinueFallbackTodosReadsInlineNumberedList(t *testing.T) {
	// 弱模型常把清单压在同一行，只按行首编号匹配会整份漏掉待办。
	text := "已定位到空指针在第 42 行。下一步：1. 修改 gateway.go 第 42 行 2. 补充单测 3. 运行 go test 验证"
	require.Equal(t, []string{"修改 gateway.go 第 42 行", "补充单测", "运行 go test 验证"}, autoContinueFallbackTodos(text, 8))
}

func TestAutoContinueFallbackTodosIgnoresSingleNumberedSentence(t *testing.T) {
	// 只有一个编号是叙述而非清单，不能当成待办清单回填。
	require.Empty(t, autoContinueFallbackTodos("下一步：1. 只需要改一处，稍等。", 8))
	// 小数会被编号模式误命中，必须跳过而不是把余数当成步骤。
	require.Empty(t, autoContinueFallbackTodos("下一步：放大到 1.5 倍再看。", 8))
}

func TestAutoContinueBuildRequestCarriesTodoList(t *testing.T) {
	decision := &AutoContinueDecision{ID: "d1", Source: "repair", Plan: "修正协议", Reason: "已定位"}
	body, err := autoContinueBuildRequestWithTodos([]byte(autoContinueTestInput), []byte(`{"output":[]}`), &autoContinueCandidate{}, decision, []string{"修正协议", "运行回归"})
	require.NoError(t, err)
	require.Contains(t, string(body), autoContinueTodoMarker)
	require.Contains(t, string(body), "1. 修正协议")
	require.Contains(t, string(body), "2. 运行回归")
}
