package analysis

import (
	"context"
	"AlgoTrade/pkg/storage"
)

// Strategy defines the lifecycle of a scientific market hypothesis
type Strategy interface {
	Name() string
	Run(ctx context.Context, db storage.Storage, manifestPath string) error
}
