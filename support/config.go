package support

import (
	"context"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/config"
)

func AWSConfig(ctx context.Context) (aws.Config, error) {
	return config.LoadDefaultConfig(ctx)
}
