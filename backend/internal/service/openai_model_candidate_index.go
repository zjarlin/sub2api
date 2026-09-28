package service

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"slices"
	"strings"
	"sync"
	"time"
)

type OpenAIRequestProtocol string

const (
	OpenAIRequestProtocolResponses       OpenAIRequestProtocol = "responses"
	OpenAIRequestProtocolChatCompletions OpenAIRequestProtocol = "chat_completions"
	OpenAIRequestProtocolMessages        OpenAIRequestProtocol = "messages"
)

type openAIRequestProtocolContextKey struct{}

// 请求入口标记实际协议；Messages 与 Chat 共用端点能力，但必须隔离候选索引。
func WithOpenAIRequestProtocol(ctx context.Context, protocol OpenAIRequestProtocol) context.Context {
	switch protocol {
	case OpenAIRequestProtocolResponses, OpenAIRequestProtocolChatCompletions, OpenAIRequestProtocolMessages:
		return context.WithValue(ctx, openAIRequestProtocolContextKey{}, protocol)
	default:
		return ctx
	}
}

func openAIRequestProtocol(ctx context.Context, capability OpenAIEndpointCapability) OpenAIRequestProtocol {
	if protocol, ok := ctx.Value(openAIRequestProtocolContextKey{}).(OpenAIRequestProtocol); ok {
		return protocol
	}
	if capability == OpenAIEndpointCapabilityResponses {
		return OpenAIRequestProtocolResponses
	}
	return OpenAIRequestProtocolChatCompletions
}

const (
	openAIModelCandidateIndexTTL        = 2 * time.Minute
	openAIModelCandidateIndexMaxEntries = 256
)

type openAIModelCandidateIndexKey struct {
	groupID     int64
	platform    string
	protocol    OpenAIRequestProtocol
	model       string
	directoryID [sha256.Size]byte
}

type openAIModelCandidateIndexEntry struct {
	accountIDs []int64
	expiresAt  time.Time
}

type openAIModelCandidateIndexCache struct {
	mu         sync.Mutex
	entries    map[openAIModelCandidateIndexKey]openAIModelCandidateIndexEntry
	now        func() time.Time
	ttl        time.Duration
	maxEntries int
}

var sharedOpenAIModelCandidateIndex = newOpenAIModelCandidateIndexCache()

func newOpenAIModelCandidateIndexCache() *openAIModelCandidateIndexCache {
	return &openAIModelCandidateIndexCache{
		entries:    make(map[openAIModelCandidateIndexKey]openAIModelCandidateIndexEntry),
		now:        time.Now,
		ttl:        openAIModelCandidateIndexTTL,
		maxEntries: openAIModelCandidateIndexMaxEntries,
	}
}

// 指纹只记录候选目录的来源；健康、配额、权限和分数仍由实时调度判定。
func openAIModelCandidateDirectoryID(accounts []Account, policy *ModelAliasPolicy) ([sha256.Size]byte, error) {
	hasher := sha256.New()
	encoder := json.NewEncoder(hasher)
	if err := encoder.Encode(policy); err != nil {
		return [sha256.Size]byte{}, err
	}
	for i := range accounts {
		account := &accounts[i]
		var models []string
		if snapshot := account.GetUpstreamSupportedModelsSnapshot(); snapshot != nil {
			models = snapshot.Models
		}
		entry := struct {
			ID       int64
			Platform string
			Mapping  map[string]string
			Catalog  []string
		}{account.ID, account.Platform, account.GetModelMapping(), models}
		if err := encoder.Encode(entry); err != nil {
			return [sha256.Size]byte{}, err
		}
	}
	return [sha256.Size]byte(hasher.Sum(nil)), nil
}

func (c *openAIModelCandidateIndexCache) candidateIDs(
	accounts []Account,
	groupID *int64,
	platform string,
	protocol OpenAIRequestProtocol,
	requestedModel string,
	policy *ModelAliasPolicy,
) ([]int64, error) {
	directoryID, err := openAIModelCandidateDirectoryID(accounts, policy)
	if err != nil {
		return nil, err
	}
	canonicalModel := policy.Canonicalize(requestedModel)
	key := openAIModelCandidateIndexKey{
		groupID:     derefGroupID(groupID),
		platform:    NormalizeOpenAICompatiblePlatform(platform),
		protocol:    protocol,
		model:       canonicalModel,
		directoryID: directoryID,
	}
	now := c.now()
	c.mu.Lock()
	if cached, ok := c.entries[key]; ok && now.Before(cached.expiresAt) {
		ids := slices.Clone(cached.accountIDs)
		c.mu.Unlock()
		return ids, nil
	}
	c.mu.Unlock()

	aliases := policy.IDs(requestedModel)
	ids := make([]int64, 0, len(accounts))
	for i := range accounts {
		if openAIAccountMaySupportModelFromDirectory(&accounts[i], aliases) {
			ids = append(ids, accounts[i].ID)
		}
	}

	c.mu.Lock()
	defer c.mu.Unlock()
	for cachedKey, cached := range c.entries {
		if !now.Before(cached.expiresAt) {
			delete(c.entries, cachedKey)
		}
	}
	if len(c.entries) >= c.maxEntries {
		var oldestKey openAIModelCandidateIndexKey
		var oldestTime time.Time
		for cachedKey, cached := range c.entries {
			if oldestTime.IsZero() || cached.expiresAt.Before(oldestTime) {
				oldestKey = cachedKey
				oldestTime = cached.expiresAt
			}
		}
		delete(c.entries, oldestKey)
	}
	c.entries[key] = openAIModelCandidateIndexEntry{accountIDs: slices.Clone(ids), expiresAt: now.Add(c.ttl)}
	return ids, nil
}

// 不明确的目录保留账号，避免索引比现有 IsModelSupported 判定更严格。
func openAIAccountMaySupportModelFromDirectory(account *Account, aliases []string) bool {
	if account == nil {
		return false
	}
	mapping := account.GetModelMapping()
	if len(mapping) == 0 {
		return true
	}
	for _, model := range aliases {
		if mappingSupportsRequestedModel(mapping, model) {
			return true
		}
		normalized := normalizeRequestedModelForLookup(account.Platform, model)
		if normalized != model && mappingSupportsRequestedModel(mapping, normalized) {
			return true
		}
	}
	if snapshot := account.GetUpstreamSupportedModelsSnapshot(); snapshot != nil {
		for _, catalogModel := range snapshot.Models {
			for _, model := range aliases {
				if strings.EqualFold(strings.TrimSpace(catalogModel), strings.TrimSpace(model)) {
					return true
				}
			}
		}
	}
	return false
}
