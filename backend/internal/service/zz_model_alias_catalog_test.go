package service

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func aliasTestAccount(id int64, passthrough bool, mapping map[string]any, catalog []string) Account {
	extra := map[string]any{}
	if passthrough {
		extra["openai_passthrough"] = true
	}
	acct := Account{
		ID: id, Platform: PlatformOpenAI, Type: AccountTypeAPIKey,
		Status: StatusActive, Schedulable: true,
		Credentials: map[string]any{"model_mapping": mapping},
		Extra:       extra,
	}
	acct.SetUpstreamSupportedModelsSnapshot(UpstreamSupportedModelsSnapshot{
		Source: "upstream", SyncedAt: time.Now().UTC().Format(time.RFC3339), Models: catalog,
	})
	return acct
}

func deepseekAliasGroups() []ModelAliasGroup {
	return []ModelAliasGroup{{
		Canonical: "deepseek-v4.1-flash",
		Aliases:   []string{"deepseek/deepseek-v4.1-flash", "cline-pass/deepseek-v4.1-flash", "DeepSeek-V4.1-Flash"},
	}}
}

// 同义词归一化：cline-pass/ 与 deepseek/ 前缀变体同属一个别名组，应互认。
func TestSynonymCatalogMatchAcceptsProviderPrefixVariant(t *testing.T) {
	acct := aliasTestAccount(866, false, nil, []string{"deepseek/deepseek-v4.1-flash"})
	acct.modelAliasGroups = deepseekAliasGroups()
	require.True(t, acct.modelAliasCatalogMatch("deepseek/deepseek-v4.1-flash", "cline-pass/deepseek-v4.1-flash"))
	require.True(t, acct.modelAliasCatalogMatchAny([]string{"other", "deepseek/deepseek-v4.1-flash"}, "cline-pass/deepseek-v4.1-flash"))
}

// 没有别名组时不做前缀剥离，拼写不同即不互认。
func TestSynonymCatalogMatchRequiresAliasGroup(t *testing.T) {
	acct := aliasTestAccount(866, false, nil, []string{"deepseek/deepseek-v4.1-flash"})
	require.False(t, acct.modelAliasCatalogMatch("deepseek/deepseek-v4.1-flash", "cline-pass/deepseek-v4.1-flash"))
}

// 别名组不会把无关模型误判为同一模型。
func TestSynonymCatalogMatchDoesNotOverMatch(t *testing.T) {
	acct := aliasTestAccount(9, false, nil, []string{"cline-pass/qwen3.8-max"})
	acct.modelAliasGroups = deepseekAliasGroups()
	require.False(t, acct.modelAliasCatalogMatch("cline-pass/qwen3.8-max", "cline-pass/deepseek-v4.1-flash"))
}

// 上游目录判定：同义命中即视为已确认支持。
func TestUpstreamCatalogSupportUsesSynonymMatch(t *testing.T) {
	acct := aliasTestAccount(866, false, nil, []string{"deepseek/deepseek-v4.1-flash"})
	acct.modelAliasGroups = deepseekAliasGroups()
	known, supported := acct.upstreamModelCatalogSupport("cline-pass/deepseek-v4.1-flash", time.Now())
	require.True(t, known)
	require.True(t, supported)

	plain := aliasTestAccount(866, false, nil, []string{"deepseek/deepseek-v4.1-flash"})
	known, supported = plain.upstreamModelCatalogSupport("cline-pass/deepseek-v4.1-flash", time.Now())
	require.True(t, known, "fresh catalog is authoritative")
	require.False(t, supported, "without an alias group the spelling mismatch is not confirmed")
}

// 改动 2：非透传账号精确显式映射，目录未列出时视为未知而非硬否定。
func TestExplicitMappingTrustsCatalogAbsenceForNonPassthrough(t *testing.T) {
	acct := aliasTestAccount(999, false,
		map[string]any{"vendor/unknown": "vendor/unknown"},
		[]string{"vendor/other"})
	require.True(t, acct.IsModelSupported("vendor/unknown"))
}

// 透传账号保持原语义：目录缺失是强证据，仍硬否定。
func TestPassthroughCatalogAbsenceRemainsHardNegative(t *testing.T) {
	acct := aliasTestAccount(999, true,
		map[string]any{"public-model": "upstream-model"},
		[]string{"upstream-model"})
	require.False(t, acct.IsModelSupported("public-model"))
}

// 通配符映射不代表管理员为该精确模型背书，目录缺失仍硬否定。
func TestWildcardMappingDoesNotTrustCatalogAbsence(t *testing.T) {
	acct := aliasTestAccount(999, false,
		map[string]any{"vendor/*": "vendor/*"},
		[]string{"vendor/other"})
	require.False(t, acct.IsModelSupported("vendor/unknown"))
}

// 改动 3：显式映射但目录未精确确认的模型进入付费探测队列。
func TestProbeTargetsExplicitMappingExcludedByCatalog(t *testing.T) {
	now := time.Now()
	acct := aliasTestAccount(866, false,
		map[string]any{"cline-pass/deepseek-v4.1-flash": "cline-pass/deepseek-v4.1-flash"},
		[]string{"deepseek/deepseek-v4.1-flash"})
	require.True(t, acct.capabilityProbeUnconfirmed("cline-pass/deepseek-v4.1-flash"))
	got := collectModelHealthProbeCandidates([]Account{acct}, nil, now, 10)
	require.Len(t, got, 1)
	require.Equal(t, int64(866), got[0].AccountID)
	require.Equal(t, "cline-pass/deepseek-v4.1-flash", got[0].Model)
}

// 目录已精确确认时不重复探测。
func TestProbeSkipsCatalogConfirmedTarget(t *testing.T) {
	now := time.Now()
	acct := aliasTestAccount(866, false,
		map[string]any{"cline-pass/deepseek-v4.1-flash": "cline-pass/deepseek-v4.1-flash"},
		[]string{"cline-pass/deepseek-v4.1-flash"})
	require.False(t, acct.capabilityProbeUnconfirmed("cline-pass/deepseek-v4.1-flash"))
	require.Empty(t, collectModelHealthProbeCandidates([]Account{acct}, nil, now, 10))
}

// 已有健康记录的模型走正常到期队列，不重复补探。
func TestProbeDoesNotDuplicateExplicitTargetWithHealthRecord(t *testing.T) {
	now := time.Now()
	recent := now.Add(-time.Hour)
	acct := aliasTestAccount(866, false,
		map[string]any{"cline-pass/deepseek-v4.1-flash": "cline-pass/deepseek-v4.1-flash"},
		[]string{"deepseek/deepseek-v4.1-flash"})
	states := []AccountModelHealthState{{
		AccountID: 866, Model: "cline-pass/deepseek-v4.1-flash", LastFailureAt: &recent,
	}}
	require.Empty(t, collectModelHealthProbeCandidates([]Account{acct}, states, now, 10))
}

// 端到端链路：别名组必须经 accountsWithModelAliases 注入到账号（非导出字段，
// 不随调度缓存 JSON 序列化），否则同义归一化在候选过滤路径上不会生效。
func TestAccountsWithModelAliasesCarriesGroupsForCatalogMatch(t *testing.T) {
	ctx := WithModelAliases(context.Background(), &ModelAliasPolicy{Groups: deepseekAliasGroups()})
	acct := aliasTestAccount(866, false, nil, []string{"deepseek/deepseek-v4.1-flash"})
	out := accountsWithModelAliases(ctx, []Account{acct})
	require.Len(t, out, 1)
	require.True(t, out[0].modelAliasCatalogMatch("deepseek/deepseek-v4.1-flash", "cline-pass/deepseek-v4.1-flash"))
	// 未经过别名注入的原始账号不互认（证明该字段确实来自请求上下文）。
	require.False(t, acct.modelAliasCatalogMatch("deepseek/deepseek-v4.1-flash", "cline-pass/deepseek-v4.1-flash"))
}
