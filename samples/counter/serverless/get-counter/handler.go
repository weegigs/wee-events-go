package main

import (
	"context"
	"errors"

	"github.com/aws/aws-lambda-go/events"
	"github.com/google/wire"
	"github.com/weegigs/wee-events-go/samples/counter"
	"github.com/weegigs/wee-events-go/stores/ds"
	"github.com/weegigs/wee-events-go/we"
)

type GatewayHandler = func(ctx context.Context, event events.APIGatewayV2HTTPRequest) (events.APIGatewayV2HTTPResponse, error)

func createHandler(loader *we.EntityLoader[counter.Counter]) GatewayHandler {
	return func(ctx context.Context, event events.APIGatewayV2HTTPRequest) (events.APIGatewayV2HTTPResponse, error) {
		namespace := event.PathParameters["namespace"]
		key := event.PathParameters["key"]

		if namespace == "" || key == "" {
			return events.APIGatewayV2HTTPResponse{
				StatusCode: 400,
			}, nil
		}

		return events.APIGatewayV2HTTPResponse{}, errors.New("not implemented")
	}
}

// TODO: add serializer

var Live = wire.NewSet(createHandler, counter.Loader, ds.Live)
