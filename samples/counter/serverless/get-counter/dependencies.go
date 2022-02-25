//go:build wireinject
// +build wireinject

package main

import (
	"context"

	"github.com/google/wire"
)

func live(ctx context.Context) (GatewayHandler, func(), error) {
	panic(wire.Build(Live))
}
