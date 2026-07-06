module github.com/greatliontech/stipulator

go 1.26.4

require google.golang.org/protobuf v1.36.11

require (
	github.com/greatliontech/stipulator/stipulate v0.0.0
	github.com/greatliontech/stipulator/stipulate/structural v0.0.0
)

replace (
	github.com/greatliontech/stipulator/stipulate => ./stipulate
	github.com/greatliontech/stipulator/stipulate/structural => ./stipulate/structural
)

require (
	github.com/modelcontextprotocol/go-sdk v1.6.1
	github.com/spf13/cobra v1.10.2
	github.com/yuin/goldmark v1.8.2
	golang.org/x/mod v0.37.0
	golang.org/x/text v0.38.0
	golang.org/x/tools v0.47.0
	pgregory.net/rapid v1.3.0
)

require (
	github.com/google/jsonschema-go v0.4.3 // indirect
	github.com/inconshreveable/mousetrap v1.1.0 // indirect
	github.com/segmentio/asm v1.1.3 // indirect
	github.com/segmentio/encoding v0.5.4 // indirect
	github.com/spf13/pflag v1.0.9 // indirect
	github.com/yosida95/uritemplate/v3 v3.0.2 // indirect
	golang.org/x/oauth2 v0.35.0 // indirect
	golang.org/x/sync v0.21.0 // indirect
	golang.org/x/sys v0.46.0 // indirect
)
