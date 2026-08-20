package vhdxworkspace

import "context"

type unavailableClient struct{}

func (unavailableClient) Call(context.Context, Request) (Response, error) {
	return Response{}, ErrBrokerUnavailable
}
