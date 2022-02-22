//go:build wireinject
// +build wireinject

package main

import (
	"context"

	"github.com/google/wire"

	we "github.com/weegigs/wee-events-go"
	"github.com/weegigs/wee-events-go/dynamo"
	"github.com/weegigs/wee-events-go/samples/counter"
	"github.com/weegigs/wee-events-go/support"
)

func DynamoCounterService(ctx context.Context, tableName dynamo.EventsTableName, randomize counter.Randomizer) (we.EntityService[counter.Counter], error) {
	panic(wire.Build(
		support.AWSConfig,
		counter.CreateCounterService,
		dynamo.EventStore,
	))
}
