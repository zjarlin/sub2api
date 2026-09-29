package service

import "context"

// 单模型恢复仅删除探测前观察到的失败记录，不扩大账号级恢复范围。
type AccountUnsupportedModelRecoveryRepository interface {
	ClearUnsupportedModelIfObserved(ctx context.Context, account *Account, model string, observed any) (bool, error)
}

func (s *UpstreamModelRefreshService) recoverProbedModel(ctx context.Context, account *Account, model string) error {
	if account == nil {
		return nil
	}
	repo, ok := s.accountRepo.(AccountUnsupportedModelRecoveryRepository)
	if !ok {
		return nil
	}
	key := observedUnsupportedModelKey(account, model)
	models, _ := account.Extra[UnsupportedModelsExtraKey].(map[string]any)
	observed, exists := models[key]
	if key == "" || !exists {
		return nil
	}
	_, err := repo.ClearUnsupportedModelIfObserved(ctx, account, key, observed)
	return err
}
