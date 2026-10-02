package graph

import "context"

func (r *queryResolver) deviceID(context.Context) (*string, error) {
	if r.Resolver.DeviceID == nil {
		return nil, nil
	}
	id := r.Resolver.DeviceID()
	if id == "" {
		return nil, nil
	}
	return &id, nil
}
