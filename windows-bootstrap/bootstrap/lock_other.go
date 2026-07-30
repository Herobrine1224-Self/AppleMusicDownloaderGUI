//go:build !windows

package bootstrap

import "context"

type NamedMutex struct{ Name string }

func (m NamedMutex) Lock(ctx context.Context) (func(), error) { return func() {}, nil }
