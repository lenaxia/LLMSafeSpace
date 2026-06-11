module github.com/lenaxia/llmsafespace/sdks/canary/mcp

go 1.23

require (
	github.com/lenaxia/llmsafespace/sdk/go v0.0.0
	github.com/lenaxia/llmsafespace/sdks/canary/go v0.0.0
)

replace (
	github.com/lenaxia/llmsafespace/sdk/go => ../../go
	github.com/lenaxia/llmsafespace/sdks/canary/go => ../go
)
