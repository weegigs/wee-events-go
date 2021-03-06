// Code generated by Wire. DO NOT EDIT.

//go:generate go run github.com/google/wire/cmd/wire
//go:build !wireinject
// +build !wireinject

package main

import (
	"context"
	"github.com/weegigs/wee-events-go/samples/counter"
	"github.com/weegigs/wee-events-go/stores/ds"
	"github.com/weegigs/wee-events-go/we"
)

// Injectors from wire.go:

func live(ctx context.Context) (we.EntityService[counter.Counter], func(), error) {
	config, err := ds.DefaultAWSConfig(ctx)
	if err != nil {
		return nil, nil, err
	}
	client := ds.Client(config)
	eventStoreTableName, err := ds.EventsTableNameFromEnvironment()
	if err != nil {
		return nil, nil, err
	}
	dynamoEventStore := ds.NewEventStore(client, eventStoreTableName)
	v := counter.PseudoRandomizer()
	entityService := NewCounterService(dynamoEventStore, v)
	return entityService, func() {
	}, nil
}

func local(ctx context.Context) (we.EntityService[counter.Counter], func(), error) {
	dynamoEventStore, err := ds.LocalDynamoStore(ctx)
	if err != nil {
		return nil, nil, err
	}
	v := counter.PseudoRandomizer()
	entityService := NewCounterService(dynamoEventStore, v)
	return entityService, func() {
	}, nil
}
