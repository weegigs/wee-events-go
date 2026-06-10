package main

import (
	"context"
	"os"

	"github.com/aws/aws-lambda-go/lambda"
	"github.com/rs/zerolog/log"
)

func main() {
	handler, cleanup, err := live(context.Background())
	if err != nil {
		log.Info().Err(err).Msg("failed to initialise handler")
		os.Exit(1)
	}
	defer cleanup()

	lambda.Start(handler)
}
