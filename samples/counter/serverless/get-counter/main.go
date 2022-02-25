package main

import (
	"context"
	"os"

	"github.com/aws/aws-lambda-go/lambda"
)

func main() {
	handler, cleanup, err := live(context.Background())
	if err != nil {
		os.Exit(1)
	}
	defer cleanup()

	lambda.Start(handler)
}
