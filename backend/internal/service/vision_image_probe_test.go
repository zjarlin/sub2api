package service

import (
	"net/http"
	"strconv"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

// 线上实测：deepseek-v4.1-flash 在多个中转都能直接读图，回答在 content 或
// reasoning_content 里。探测必须认这两种回包，否则会把支持视觉的模型判成不支持。
func TestJudgeVisionImageProbeRecognizesRealAnswers(t *testing.T) {
	for _, tc := range []struct {
		name string
		body string
		want bool
	}{
		{"content_red", `{"choices":[{"message":{"role":"assistant","content":"Red"}}]}`, true},
		{"reasoning_only", `{"choices":[{"message":{"role":"assistant","content":"","reasoning_content":"The image is a solid block of light red/pinkish color."}}]}`, true},
		{"content_parts", `{"choices":[{"message":{"content":[{"type":"text","text":"Crimson"}]}}]}`, true},
		{"chinese", `{"choices":[{"message":{"content":"红色"}}]}`, true},
		{"refuses_to_look", `{"choices":[{"message":{"content":"I cannot see the image."}}]}`, false},
		{"empty", `{"choices":[{"message":{"content":""}}]}`, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			result := judgeVisionImageProbe("deepseek-v4.1-flash", http.StatusOK, []byte(tc.body))
			require.True(t, result.Conclusive)
			require.Equal(t, tc.want, result.SupportsImage)
		})
	}
}

// 真·纯文本上游：能正常回答文本问题，但对图片只回套话。必须判为不支持，
// 否则探测就失去区分力，任何模型都会被标成支持视觉。
func TestJudgeVisionImageProbeRejectsTextOnlyAnswers(t *testing.T) {
	for _, answer := range []string{
		"I'm sorry, I cannot view or process images.",
		"Please describe the image in text and I will help.",
		"As a text-only model I have no access to the attachment.",
	} {
		body := `{"choices":[{"message":{"role":"assistant","content":` + strconv.Quote(answer) + `}}]}`
		result := judgeVisionImageProbe("text-only-model", http.StatusOK, []byte(body))
		require.True(t, result.Conclusive)
		require.False(t, result.SupportsImage, "纯文本模型的套话不能算读到图: %q", answer)
	}
}

// 鉴权、限流、5xx、模型不存在都不构成能力证据：必须保持 unknown，不能写死“不支持”。
func TestJudgeVisionImageProbeKeepsUnknownForInconclusiveResponses(t *testing.T) {
	for _, tc := range []struct {
		name   string
		status int
		body   string
	}{
		{"unauthorized", http.StatusUnauthorized, `{"error":{"message":"invalid api key"}}`},
		{"rate_limited", http.StatusTooManyRequests, `{"error":{"message":"quota exhausted"}}`},
		{"upstream_5xx", http.StatusBadGateway, `{"error":{"message":"bad gateway"}}`},
		{"model_missing", http.StatusBadRequest, `{"error":{"code":"model_not_found","message":"unknown model"}}`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			result := judgeVisionImageProbe("deepseek-v4.1-flash", tc.status, []byte(tc.body))
			require.False(t, result.Conclusive, "无证据时不能下结论")
		})
	}
}

// 上游明确说“不支持图片输入”是有效的负面证据，应立刻落标，避免继续浪费辅助预算。
func TestJudgeVisionImageProbeTreatsExplicitRejectionAsNegative(t *testing.T) {
	for _, body := range []string{
		`{"error":{"message":"This model does not support image input"}}`,
		`{"error":{"message":"该模型仅支持文本输入"}}`,
	} {
		result := judgeVisionImageProbe("some-text-model", http.StatusBadRequest, []byte(body))
		require.True(t, result.Conclusive)
		require.False(t, result.SupportsImage)
	}
}

// 能力快照写入必须保留既有模型条目，并按探测结论设置 input_modalities。
func TestApplyVisionImageProbeResultWritesModalities(t *testing.T) {
	for _, supports := range []bool{true, false} {
		t.Run(map[bool]string{true: "supported", false: "text_only"}[supports], func(t *testing.T) {
			acct := visionTestAccount(1, "deepseek-v4.1-flash")
			account := &acct
			account.SetUpstreamModelMetadataSnapshot(UpstreamModelMetadataSnapshot{
				Source: "upstream",
				Models: map[string]UpstreamModelMetadata{"other-model": {ID: "other-model", InputModalities: []string{"text"}}},
			})
			repo := &snapshotUpdateAccountRepo{stubOpenAIAccountRepo: stubOpenAIAccountRepo{accounts: []Account{*account}}, updateExtraCalls: make(chan map[string]any, 1)}
			svc := &AccountTestService{accountRepo: repo}
			err := svc.ApplyVisionImageProbeResult(t.Context(), account, "deepseek-v4.1-flash",
				VisionImageProbeResult{SupportsImage: supports, Conclusive: true, StatusCode: http.StatusOK})
			require.NoError(t, err)
			snapshot := account.GetUpstreamModelMetadataSnapshot()
			require.Equal(t, VisionImageProbeSource, snapshot.Source)
			require.Contains(t, snapshot.Models, "other-model", "既有条目必须保留")
			modalities := snapshot.Models["deepseek-v4.1-flash"].InputModalities
			require.Equal(t, []string{"text"}, modalities[:1])
			if supports {
				require.Contains(t, modalities, "image")
				require.True(t, accountHasNativeVision(account, "deepseek-v4.1-flash"))
			} else {
				require.NotContains(t, modalities, "image")
				require.True(t, accountHasKnownTextOnlyInput(account, "deepseek-v4.1-flash"))
			}
		})
	}
}

// 未定论时不得改写账号能力。
func TestApplyVisionImageProbeResultIgnoresInconclusive(t *testing.T) {
	acct := visionTestAccount(1, "deepseek-v4.1-flash")
	account := &acct
	before := account.GetUpstreamModelMetadataSnapshot()
	svc := &AccountTestService{accountRepo: &snapshotUpdateAccountRepo{stubOpenAIAccountRepo: stubOpenAIAccountRepo{accounts: []Account{*account}}, updateExtraCalls: make(chan map[string]any, 1)}}
	require.NoError(t, svc.ApplyVisionImageProbeResult(t.Context(), account, "deepseek-v4.1-flash", VisionImageProbeResult{Conclusive: false}))
	after := account.GetUpstreamModelMetadataSnapshot()
	require.Equal(t, before, after)
	require.Empty(t, strings.TrimSpace(strings.Join(account.GetUpstreamModelMetadataSnapshot().Models["deepseek-v4.1-flash"].InputModalities, ",")))
}
