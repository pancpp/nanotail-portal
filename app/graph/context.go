package graph

import "context"

type ContextValue struct {
	UserPID int64
	// AfterResponse registers work to run after the GraphQL response has been
	// written and flushed, including when the response write fails.
	AfterResponse func(func())
}

const QUERY_CONTEXT_KEY string = "query-ctx"

func queryContextValue(ctx context.Context) *ContextValue {
	raw, _ := ctx.Value(QUERY_CONTEXT_KEY).(*ContextValue)
	return raw
}
