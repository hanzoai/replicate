module github.com/hanzoai/replicate

go 1.26.5

require (
	github.com/MadAppGang/httplog v1.3.0
	github.com/aws/aws-sdk-go-v2 v1.41.5
	github.com/aws/aws-sdk-go-v2/config v1.32.6
	github.com/aws/aws-sdk-go-v2/credentials v1.19.6
	github.com/aws/aws-sdk-go-v2/feature/s3/manager v1.20.18
	github.com/aws/aws-sdk-go-v2/service/s3 v1.97.3
	github.com/aws/smithy-go v1.24.2
	github.com/dustin/go-humanize v1.0.1
	github.com/hanzoai/ltx v0.5.1
	github.com/hashicorp/golang-lru/v2 v2.0.7
	github.com/mark3labs/mcp-go v0.32.0
	github.com/mattn/go-shellwords v1.0.12
	github.com/nats-io/nats.go v1.44.0
	github.com/pkg/sftp v1.13.6
	github.com/psanford/sqlite3vfs v0.0.0-20251127171934-4e34e03a991a // direct
	github.com/studio-b12/gowebdav v0.11.0
	golang.org/x/crypto v0.54.0
	golang.org/x/sys v0.47.0
	gopkg.in/yaml.v2 v2.4.0
	modernc.org/sqlite v1.51.0
)

require (
	github.com/fsnotify/fsnotify v1.7.0
	github.com/ncruces/go-strftime v1.0.0 // indirect
	github.com/remyoudompheng/bigfft v0.0.0-20230129092748-24d4a6f8daec // indirect
	golang.org/x/sync v0.22.0
	modernc.org/libc v1.72.3 // indirect
	modernc.org/mathutil v1.7.1 // indirect
	modernc.org/memory v1.11.0 // indirect
)

require (
	github.com/hanzoai/sqlite v0.5.2
	github.com/markusmobius/go-dateparser v1.2.4
)

require (
	github.com/hanzoai/lz4/v4 v4.1.22
	github.com/lmittmann/tint v1.1.3
	github.com/luxfi/age v1.6.0 // fail-closed encryption; forced up from v1.4.0 (its tag was re-published upstream, so main's go.sum hash no longer verifies) to the blessed v1.6.0 — encrypt/decrypt/restore round-trip verified by the e2e test
	github.com/luxfi/zap v1.2.6
	github.com/mattn/go-isatty v0.0.22
	github.com/stretchr/testify v1.11.1
	github.com/zeebo/blake3 v0.2.4
	golang.org/x/crypto/x509roots/fallback v0.0.0-20260209214922-2f26647a795e
)

require (
	github.com/gorilla/rpc v1.2.1 // indirect
	github.com/hanzoai/csqlite v0.1.2 // indirect
	github.com/hanzoai/sqlcipher v0.1.1 // indirect
)

require (
	filippo.io/hpke v0.4.0 // indirect
	github.com/TylerBrock/colorjson v0.0.0-20200706003622-8a50f05110d2 // indirect
	github.com/aws/aws-sdk-go-v2/aws/protocol/eventstream v1.7.8 // indirect
	github.com/aws/aws-sdk-go-v2/feature/ec2/imds v1.18.16 // indirect
	github.com/aws/aws-sdk-go-v2/internal/configsources v1.4.21 // indirect
	github.com/aws/aws-sdk-go-v2/internal/endpoints/v2 v2.7.21 // indirect
	github.com/aws/aws-sdk-go-v2/internal/ini v1.8.4 // indirect
	github.com/aws/aws-sdk-go-v2/internal/v4a v1.4.22 // indirect
	github.com/aws/aws-sdk-go-v2/service/internal/accept-encoding v1.13.7 // indirect
	github.com/aws/aws-sdk-go-v2/service/internal/checksum v1.9.13 // indirect
	github.com/aws/aws-sdk-go-v2/service/internal/presigned-url v1.13.21 // indirect
	github.com/aws/aws-sdk-go-v2/service/internal/s3shared v1.19.21 // indirect
	github.com/aws/aws-sdk-go-v2/service/signin v1.0.4 // indirect
	github.com/aws/aws-sdk-go-v2/service/sso v1.30.8 // indirect
	github.com/aws/aws-sdk-go-v2/service/ssooidc v1.35.12 // indirect
	github.com/aws/aws-sdk-go-v2/service/sts v1.41.5 // indirect
	github.com/cenkalti/backoff v2.2.1+incompatible // indirect
	github.com/cloudflare/circl v1.6.3 // indirect
	github.com/davecgh/go-spew v1.1.2-0.20180830191138-d8f796af33cc // indirect
	github.com/fatih/color v1.13.0 // indirect
	github.com/google/uuid v1.6.1-0.20241114170450-2d3c2a9cc518 // indirect
	github.com/grandcat/zeroconf v1.0.0 // indirect
	github.com/hablullah/go-hijri v1.0.2 // indirect
	github.com/hablullah/go-juliandays v1.0.0 // indirect
	github.com/jalaali/go-jalaali v0.0.0-20210801064154-80525e88d958 // indirect
	github.com/klauspost/compress v1.18.6 // indirect
	github.com/klauspost/cpuid/v2 v2.3.0 // indirect
	github.com/kr/fs v0.1.0 // indirect
	github.com/luxfi/accel v1.2.4 // indirect
	github.com/luxfi/crypto v1.20.2 // indirect
	github.com/luxfi/mdns v0.1.1 // indirect
	github.com/luxfi/metric v1.8.1
	github.com/magefile/mage v1.14.0 // indirect
	github.com/mattn/go-colorable v0.1.14 // indirect
	github.com/miekg/dns v1.1.72 // indirect
	github.com/nats-io/nkeys v0.4.11 // indirect
	github.com/nats-io/nuid v1.0.1 // indirect
	github.com/pmezard/go-difflib v1.0.1-0.20181226105442-5d4384ee4fb2 // indirect
	github.com/spf13/cast v1.7.1 // indirect
	github.com/tetratelabs/wazero v1.2.1 // indirect
	github.com/wasilibs/go-re2 v1.3.0 // indirect
	github.com/yosida95/uritemplate/v3 v3.0.2 // indirect
	golang.org/x/mod v0.37.0 // indirect
	golang.org/x/net v0.56.0 // indirect
	golang.org/x/text v0.40.0 // indirect
	golang.org/x/tools v0.47.0 // indirect
	google.golang.org/protobuf v1.36.11 // indirect
	gopkg.in/yaml.v3 v3.0.1 // indirect
)
