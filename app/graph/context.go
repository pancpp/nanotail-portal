package graph

import "context"

type ContextValue struct {
	UserPID int64
}

const QUERY_CONTEXT_KEY string = "query-ctx"

func queryContextValue(ctx context.Context) *ContextValue {
	raw, _ := ctx.Value(QUERY_CONTEXT_KEY).(*ContextValue)
	return raw
}
