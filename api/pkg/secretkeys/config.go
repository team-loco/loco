package secretkeys

import "fmt"

// Config selects the key provider and holds what it needs to start.
type Config struct {
	Provider  string
	LocalKeys []LocalKey
}

// New builds the provider Config selects.
func New(cfg Config) (Provider, error) {
	switch cfg.Provider {
	case ProviderLocal:
		local, err := NewLocal(cfg.LocalKeys)
		if err != nil {
			return nil, fmt.Errorf("local secrets key provider: %w", err)
		}
		return local, nil
	default:
		return nil, fmt.Errorf("%w: %q", ErrUnknownProvider, cfg.Provider)
	}
}
