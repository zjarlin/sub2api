//go:build unit

package service

import "context"

// catalogExtraRepo 只实现签名目录同步需要的 UpdateExtra。
type catalogExtraRepo struct {
	AccountRepository
	updates []map[string]any
	err     error
}

func (r *catalogExtraRepo) UpdateExtra(_ context.Context, _ int64, updates map[string]any) error {
	if r.err != nil {
		return r.err
	}
	r.updates = append(r.updates, updates)
	return nil
}

type stubQoderCatalogFetcher struct {
	body []byte
	err  error
}

func (s stubQoderCatalogFetcher) FetchCatalog(context.Context, string, string) ([]byte, error) {
	if s.err != nil {
		return nil, s.err
	}
	return s.body, nil
}
