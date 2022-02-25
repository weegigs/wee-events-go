//go:build wireinject
// +build wireinject

package main

import (
	"context"

	"github.com/google/wire"
)

func live(ctx context.Context) (CounterService, func(), error) {
	panic(wire.Build(Live))
}

func local(ctx context.Context) (CounterService, func(), error) {
	panic(wire.Build(Local))
}
