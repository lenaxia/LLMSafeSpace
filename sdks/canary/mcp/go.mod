module github.com/lenaxia/llmsafespaces/sdks/canary/mcp

go 1.23

require (
	github.com/lenaxia/llmsafespaces/sdk/go v0.0.0
	github.com/lenaxia/llmsafespaces/sdks/canary/go v0.0.0
)

replace (
	github.com/lenaxia/llmsafespaces/sdk/go => ../../go
	github.com/lenaxia/llmsafespaces/sdks/canary/go => ../go
)
