package engine

import (
	"context"
	"errors"
	"os"

	"github.com/mulcahyio/libre-rws/modules/dvt/internal/redact"
)

type secretsKey struct{}

// WithSecrets returns a context carrying the run's configured secrets.
// Every process LibreDVT starts under that context is checked first: a
// secret in its argument vector or environment refuses the start
// (DVT-SRS-153). Secrets reach the worker only as file paths.
func WithSecrets(ctx context.Context, s *redact.Set) context.Context {
	return context.WithValue(ctx, secretsKey{}, s)
}

func secretsFrom(ctx context.Context) *redact.Set {
	s, _ := ctx.Value(secretsKey{}).(*redact.Set)
	return s
}

// errSecretInArgs never names the secret or the argument holding it.
var errSecretInArgs = errors.New("refused to start the process: a configured secret appears in its arguments or environment (DVT-SRS-153)")

// checkNoSecrets applies DVT-SRS-153 to one process start.
func checkNoSecrets(ctx context.Context, name string, args []string) error {
	s := secretsFrom(ctx)
	if s.Empty() {
		return nil
	}
	for _, a := range append([]string{name}, args...) {
		if s.Contains(a) {
			return errSecretInArgs
		}
	}
	for _, e := range os.Environ() {
		if s.Contains(e) {
			return errSecretInArgs
		}
	}
	return nil
}
