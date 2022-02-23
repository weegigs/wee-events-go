//go:build wireinject
// +build wireinject

package main

import (
	"context"

	"github.com/google/wire"
	we "github.com/weegigs/wee-events-go"
	"github.com/weegigs/wee-events-go/samples/counter"
)

func live(ctx context.Context) (we.EntityService[counter.Counter], func(), error) {
	panic(wire.Build(counter.Live))
}

func local(ctx context.Context) (we.EntityService[counter.Counter], func(), error) {
	panic(wire.Build(counter.Local))
}
