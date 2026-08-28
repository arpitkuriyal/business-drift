package logging

import (
	"go.uber.org/zap"
)

func New(environment string) (*zap.Logger, error) {
	if environment == "development" {
		return zap.NewDevelopment()
	}
	return zap.NewProduction()
}
