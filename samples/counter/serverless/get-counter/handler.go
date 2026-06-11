package main

import (
	"context"
	"errors"

	"github.com/aws/aws-lambda-go/events"
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

// live composes the handler against the ambient AWS environment. JSON names
// the handler's write encoding at the composition root (ENCODING-S2.R4):
// it is the recommended interop choice.
func live(ctx context.Context) (GatewayHandler, error) {
	cfg, err := ds.DefaultAWSConfig(ctx)
	if err != nil {
		return nil, err
	}

	table, err := ds.EventsTableNameFromEnvironment()
	if err != nil {
		return nil, err
	}

	store, err := ds.NewEventStore(ds.Client(cfg), table, we.MakeJSONEncoder())
	if err != nil {
		return nil, err
	}

	return createHandler(counter.Loader(store)), nil
}
