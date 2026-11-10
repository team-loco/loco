package secretkeys

import "errors"

var (
	ErrWrongProvider = errors.New("wrapped key belongs to another provider")
	ErrDecrypt       = errors.New("decryption failed")
	ErrKeySize       = errors.New("key is not 32 bytes")
	ErrCacheTTL      = errors.New("data key cache ttl must be positive")
)
