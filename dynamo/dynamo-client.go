package dynamo

import (
	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/dynamodb"
)

func Client(cfg aws.Config) *dynamodb.Client {
	return dynamodb.NewFromConfig(cfg)
}
