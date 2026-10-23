module github.com/team-loco/loco/observability-proxy

go 1.27.3

require (
	connectrpc.com/connect/v2 v2.0.0
	connectrpc.com/cors v0.1.0
	github.com/ClickHouse/clickhouse-go/v2 v2.49.0
	github.com/dgraph-io/ristretto/v2 v2.4.2
	github.com/go-jose/go-jose/v4 v4.1.5
	github.com/google/uuid v1.6.0
	github.com/open-telemetry/opentelemetry-collector-contrib/exporter/clickhouseexporter v0.163.0
	github.com/pressly/goose/v3 v3.28.0
	github.com/prometheus/client_golang v1.25.0
	github.com/rs/cors v1.11.1
	github.com/team-loco/loco v0.0.0
	go.opentelemetry.io/collector/component v1.69.0
	go.opentelemetry.io/collector/config/configoptional v1.69.0
	go.opentelemetry.io/collector/consumer v1.69.0
	go.opentelemetry.io/collector/consumer/consumererror v0.163.0
	go.opentelemetry.io/collector/exporter v1.69.0
	go.opentelemetry.io/collector/exporter/exporterhelper v0.163.0
	go.opentelemetry.io/collector/pdata v1.69.0
	go.opentelemetry.io/otel/metric v1.47.0
	go.opentelemetry.io/otel/trace v1.47.0
	go.uber.org/zap v1.28.0
	google.golang.org/genproto/googleapis/rpc v0.0.0-20260831171406-18b4a7587f8a
	google.golang.org/grpc v1.84.0
	google.golang.org/protobuf v1.36.12
)

require (
	buf.build/gen/go/bufbuild/protovalidate/protocolbuffers/go v1.36.12-20260825204119-511051f7f437.2 // indirect
	github.com/ClickHouse/ch-go v0.74.0 // indirect
	github.com/andybalholm/brotli v1.2.6 // indirect
	github.com/beorn7/perks v1.0.1 // indirect
	github.com/cenkalti/backoff/v7 v7.0.1 // indirect
	github.com/cespare/xxhash/v2 v2.3.0 // indirect
	github.com/dustin/go-humanize v1.0.1 // indirect
	github.com/foxboron/go-tpm-keyfiles v0.0.0-20250903184740-5d135037bd4d // indirect
	github.com/go-faster/city v1.0.1 // indirect
	github.com/go-faster/errors v0.8.0 // indirect
	github.com/go-viper/mapstructure/v2 v2.5.0 // indirect
	github.com/gobwas/glob v0.2.3 // indirect
	github.com/google/go-tpm v0.9.8 // indirect
	github.com/hashicorp/go-version v1.9.0 // indirect
	github.com/hashicorp/golang-lru/v2 v2.0.7 // indirect
	github.com/json-iterator/go v1.1.12 // indirect
	github.com/klauspost/compress v1.20.1 // indirect
	github.com/knadh/koanf/maps v0.1.3 // indirect
	github.com/knadh/koanf/providers/confmap v1.0.1 // indirect
	github.com/knadh/koanf/v2 v2.3.8 // indirect
	github.com/kylelemons/godebug v1.1.0 // indirect
	github.com/mfridman/interpolate v0.0.2 // indirect
	github.com/mitchellh/copystructure v1.2.0 // indirect
	github.com/mitchellh/reflectwalk v1.0.2 // indirect
	github.com/modern-go/concurrent v0.0.0-20180306012644-bacd9c7ef1dd // indirect
	github.com/modern-go/reflect2 v1.0.3-0.20250322232337-35a7c28c31ee // indirect
	github.com/munnerz/goautoneg v0.0.0-20191010083416-a7dc8b61c822 // indirect
	github.com/open-telemetry/opentelemetry-collector-contrib/internal/coreinternal v0.163.0 // indirect
	github.com/paulmach/orb v0.13.0 // indirect
	github.com/pierrec/lz4/v4 v4.1.31 // indirect
	github.com/prometheus/client_model v0.6.3 // indirect
	github.com/prometheus/common v0.72.0 // indirect
	github.com/prometheus/procfs v0.22.0 // indirect
	github.com/segmentio/asm v1.2.1 // indirect
	github.com/sethvargo/go-retry v0.4.0 // indirect
	github.com/shopspring/decimal v1.4.0 // indirect
	github.com/stretchr/testify v1.12.1 // indirect
	go.opentelemetry.io/collector/client v1.69.0 // indirect
	go.opentelemetry.io/collector/config/configopaque v1.69.0 // indirect
	go.opentelemetry.io/collector/config/configretry v1.69.0 // indirect
	go.opentelemetry.io/collector/config/configtls v1.69.0 // indirect
	go.opentelemetry.io/collector/confmap v1.69.0 // indirect
	go.opentelemetry.io/collector/consumer/consumererror/xconsumererror v0.163.0 // indirect
	go.opentelemetry.io/collector/consumer/xconsumer v0.163.0 // indirect
	go.opentelemetry.io/collector/exporter/exporterhelper/xexporterhelper v0.163.0 // indirect
	go.opentelemetry.io/collector/exporter/xexporter v0.163.0 // indirect
	go.opentelemetry.io/collector/extension v1.69.0 // indirect
	go.opentelemetry.io/collector/extension/xextension v0.163.0 // indirect
	go.opentelemetry.io/collector/featuregate v1.69.0 // indirect
	go.opentelemetry.io/collector/internal/componentalias v0.163.0 // indirect
	go.opentelemetry.io/collector/pdata/pprofile v0.163.0 // indirect
	go.opentelemetry.io/collector/pdata/xpdata v0.163.0 // indirect
	go.opentelemetry.io/collector/pipeline v1.69.0 // indirect
	go.opentelemetry.io/collector/pipeline/xpipeline v0.163.0 // indirect
	go.opentelemetry.io/otel v1.47.0 // indirect
	go.uber.org/multierr v1.11.0 // indirect
	go.yaml.in/yaml/v3 v3.0.5 // indirect
	golang.org/x/crypto v0.58.0 // indirect
	golang.org/x/net v0.61.0 // indirect
	golang.org/x/sync v0.24.0 // indirect
	golang.org/x/sys v0.49.0 // indirect
	golang.org/x/text v0.43.0 // indirect
)

replace github.com/team-loco/loco => ../
