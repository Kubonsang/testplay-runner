package vhdxworkspace

import (
	"context"
	"fmt"
)

// DirectClient exercises the complete broker request path without bypassing
// authorization. It is useful for unit tests and service-host composition;
// production CLI clients use the Windows named-pipe transport.
type DirectClient struct {
	Broker    *Broker
	CallerSID string
}

func (client DirectClient) Call(ctx context.Context, request Request) (Response, error) {
	if client.Broker == nil {
		return Response{}, ErrBrokerUnavailable
	}
	response := client.Broker.Handle(ctx, client.CallerSID, request)
	if !response.OK {
		if response.Error != nil {
			return response, response.Error
		}
		return response, fmt.Errorf("broker request failed")
	}
	return response, nil
}

func NewRequest(operation, requestID string) Request {
	return Request{SchemaVersion: ProtocolSchemaVersion, Operation: operation, RequestID: requestID}
}
